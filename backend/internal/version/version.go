package version

var (
	Version = "1.1.34"
	Repo    = "FenhaoLost/eyves-vm-panel"
)

func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
