package orca

// HostDesktop returns the Orca desktop app of the machine the CLI runs on.
func HostDesktop() Desktop { return noDesktop{} }

type noDesktop struct{}

func (noDesktop) Version() (string, error) { return "", nil }
