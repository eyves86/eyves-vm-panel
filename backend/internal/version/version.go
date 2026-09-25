package version

var (
	Version = "1.4.0"
	Repo    = "FenhaoLost/eyves-vm-panel"
)

func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
