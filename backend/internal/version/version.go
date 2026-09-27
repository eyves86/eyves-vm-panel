package version

var (
	Version = "2.2.2"
	Repo    = "FenhaoLost/eyves-vm-panel"
)

func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
