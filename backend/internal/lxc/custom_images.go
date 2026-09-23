package lxc

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"eyvescloud/internal/safehttp"
)

type CustomImageDownloadProgress struct {
	Stage           string
	DownloadedBytes int64
	TotalBytes      int64
	Percent         int
}

type CustomImageDownloadProgressFunc func(CustomImageDownloadProgress)

func CustomImagePath(id string) string {
	template := FindTemplate(id)
	if template == nil || !template.Custom {
		return filepath.Join("/var/cache/lxc/download/custom", "__invalid_image_id__", "rootfs.tar")
	}
	return filepath.Join("/var/cache/lxc/download/custom", template.ID, "rootfs.tar")
}

func CustomImageDownloadedInfo(id string) (bool, int64) {
	info, err := os.Stat(CustomImagePath(id))
	if err != nil || info.IsDir() {
		return false, 0
	}
	return true, info.Size()
}

func DeleteCustomImage(id string) error {
	template := FindTemplate(id)
	if template == nil || !template.Custom {
		return fmt.Errorf("custom LXC image not found")
	}
	return os.RemoveAll(filepath.Dir(CustomImagePath(id)))
}

func DownloadCustomImageWithProgress(ctx context.Context, template Template, progress CustomImageDownloadProgressFunc) error {
	if !template.Custom {
		return fmt.Errorf("template is not a custom LXC image")
	}
	target := CustomImagePath(template.ID)
	if ok, _ := CustomImageDownloadedInfo(template.ID); ok {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	tmp := target + ".tmp"
	_ = os.Remove(tmp)
	if err := downloadCustomRootfs(ctx, template.URL, tmp, progress); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if template.SHA256 != "" {
		if err := verifyCustomRootfsSHA256(tmp, template.SHA256); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if progress != nil {
		progress(CustomImageDownloadProgress{Stage: "validating", Percent: 100})
	}
	if err := ValidateCustomRootfsArchive(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(target, 0644)
}

func downloadCustomRootfs(ctx context.Context, sourceURL, target string, progress CustomImageDownloadProgressFunc) error {
	response, err := safehttp.Get(ctx, sourceURL, "EyvesCloud/1.0 LXC image downloader", 30*time.Minute)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download failed: %s", response.Status)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	total := response.ContentLength
	buffer := make([]byte, 128*1024)
	var downloaded int64
	for {
		count, readErr := response.Body.Read(buffer)
		if count > 0 {
			if _, err := file.Write(buffer[:count]); err != nil {
				return err
			}
			downloaded += int64(count)
			if progress != nil {
				percent := 0
				if total > 0 {
					percent = int(downloaded * 100 / total)
					if percent > 100 {
						percent = 100
					}
				}
				progress(CustomImageDownloadProgress{
					Stage:           "downloading",
					DownloadedBytes: downloaded,
					TotalBytes:      total,
					Percent:         percent,
				})
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return file.Sync()
}

func verifyCustomRootfsSHA256(filePath, expected string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, strings.TrimSpace(expected)) {
		return fmt.Errorf("SHA-256 mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func ValidateCustomRootfsArchive(archivePath string) error {
	stream, closeFn, err := openTarStream(archivePath)
	if err != nil {
		return err
	}
	defer closeFn()
	tr := tar.NewReader(stream)
	entries := make([]string, 0, 4096)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("invalid rootfs archive: %v", err)
		}
		if len(entries) >= 2_000_000 {
			return fmt.Errorf("rootfs archive contains too many entries")
		}
		entries = append(entries, hdr.Name)
	}
	return validateCustomRootfsEntries(entries)
}

func validateCustomRootfsEntries(entries []string) error {
	hasInit := false
	for _, entry := range entries {
		entry = strings.TrimSpace(strings.ReplaceAll(entry, "\\", "/"))
		entry = strings.TrimPrefix(entry, "./")
		if entry == "" || entry == "." {
			continue
		}
		if strings.HasPrefix(entry, "/") {
			return fmt.Errorf("rootfs archive contains an absolute path: %s", entry)
		}
		clean := path.Clean(entry)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("rootfs archive contains path traversal: %s", entry)
		}
		switch strings.TrimSuffix(clean, "/") {
		case "sbin/init", "usr/lib/systemd/systemd", "lib/systemd/systemd", "bin/busybox", "bin/sh":
			hasInit = true
		}
	}
	if len(entries) == 0 {
		return fmt.Errorf("rootfs archive is empty")
	}
	if !hasInit {
		return fmt.Errorf("rootfs archive does not contain a supported init")
	}
	return nil
}

func ExtractCustomRootfs(templateID, destination string) error {
	template := FindTemplate(templateID)
	if template == nil || !template.Custom {
		return fmt.Errorf("custom LXC image not found: %s", templateID)
	}
	archive := CustomImagePath(template.ID)
	if ok, _ := CustomImageDownloadedInfo(template.ID); !ok {
		return fmt.Errorf("custom LXC image is not downloaded: %s", templateID)
	}
	if err := ValidateCustomRootfsArchive(archive); err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	if err := extractTarGzSafe(archive, destination); err != nil {
		return fmt.Errorf("failed to extract custom LXC rootfs: %v", err)
	}
	if err := secureExtractedRootfs(destination); err != nil {
		return err
	}
	if !rootfsHasInit(destination) {
		return fmt.Errorf("extracted custom LXC rootfs is invalid: init not found")
	}
	return nil
}

// openTarStream opens a possibly-compressed tar archive and returns a reader
// positioned at the tar stream. gzip and xz are detected by magic bytes; xz
// decompression shells out to the system xz tool, while every entry is still
// validated by the Go extraction code.
func openTarStream(archivePath string) (io.Reader, func(), error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, nil, err
	}
	br := bufio.NewReader(f)
	magic, _ := br.Peek(6)
	if len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			f.Close()
			return nil, nil, fmt.Errorf("invalid gzip stream: %v", err)
		}
		return gz, func() { _ = gz.Close(); _ = f.Close() }, nil
	}
	if len(magic) >= 6 && bytes.Equal(magic[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}) {
		cmd := exec.Command("xz", "-dc")
		cmd.Stdin = br
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			f.Close()
			return nil, nil, err
		}
		return stdout, func() { _ = stdout.Close(); _ = cmd.Wait(); _ = f.Close() }, nil
	}
	return br, func() { _ = f.Close() }, nil
}

// extractTarGzSafe extracts a tar archive (gzip/xz/plain) to dest while
// validating every entry in real time: absolute paths, path traversal and
// symlink/hardlink targets that escape dest are rejected before anything is
// written, preventing zip-slip style attacks on untrusted rootfs images.
func extractTarGzSafe(archivePath, dest string) error {
	stream, closeFn, err := openTarStream(archivePath)
	if err != nil {
		return err
	}
	defer closeFn()
	tr := tar.NewReader(stream)
	destAbs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		clean := path.Clean(strings.TrimPrefix(strings.ReplaceAll(hdr.Name, "\\", "/"), "./"))
		if strings.HasPrefix(clean, "/") {
			return fmt.Errorf("rootfs archive contains an absolute path: %s", hdr.Name)
		}
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("rootfs archive contains path traversal: %s", hdr.Name)
		}
		target := filepath.Join(destAbs, filepath.FromSlash(clean))
		if !withinDir(destAbs, target) {
			return fmt.Errorf("rootfs archive entry escapes destination: %s", hdr.Name)
		}
		info := hdr.FileInfo()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			_ = os.Chmod(target, info.Mode())
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
			_ = os.Chmod(target, info.Mode())
		case tar.TypeSymlink:
			linkAbs := filepath.Join(filepath.Dir(target), filepath.FromSlash(hdr.Linkname))
			if !withinDir(destAbs, linkAbs) {
				return fmt.Errorf("rootfs archive symlink escapes destination: %s -> %s", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkAbs := filepath.Join(destAbs, filepath.FromSlash(hdr.Linkname))
			if !withinDir(destAbs, linkAbs) {
				return fmt.Errorf("rootfs archive hardlink escapes destination: %s -> %s", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			if err := os.Link(linkAbs, target); err != nil {
				return err
			}
		default:
			// Ignore special entries (fifos, devices).
			continue
		}
	}
	return nil
}

// withinDir reports whether target resolves to a path inside root.
func withinDir(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

func secureExtractedRootfs(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(filePath)
		if err != nil {
			return err
		}
		var resolved string
		if filepath.IsAbs(target) {
			resolved = filepath.Join(root, strings.TrimLeft(filepath.ToSlash(target), "/"))
			relative, err := filepath.Rel(filepath.Dir(filePath), resolved)
			if err != nil {
				return err
			}
			if err := os.Remove(filePath); err != nil {
				return err
			}
			if err := os.Symlink(relative, filePath); err != nil {
				return err
			}
		} else {
			resolved = filepath.Join(filepath.Dir(filePath), target)
		}
		relativeToRoot, err := filepath.Rel(root, filepath.Clean(resolved))
		if err != nil {
			return err
		}
		if relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("rootfs symlink escapes the archive root: %s -> %s", filePath, target)
		}
		return nil
	})
}
