package version

var (
	Version = "1.5.1"
	Repo    = "FenhaoLost/eyves-vm-panel"
)

func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
