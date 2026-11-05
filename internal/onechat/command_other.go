//go:build !linux

package onechat

func sessionProcessIdentity(int) string { return "" }
