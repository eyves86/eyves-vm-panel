package version

var (
	Version = "2.0.0"
	Repo    = "FenhaoLost/eyves-vm-panel"
)

func Current() string {
	if Version == "" {
		return "dev"
	}
	return Version
}
