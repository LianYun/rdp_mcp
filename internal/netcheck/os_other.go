//go:build !darwin

package netcheck

func localNetworkPrivacy() bool { return false }
