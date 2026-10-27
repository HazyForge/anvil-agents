//go:build !windows

package desktop

import (
	"fmt"
	"os"
	"syscall"
)

func onechatTokenOwner(_ *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("one chat host-token must be owned by the current user; fix its ownership")
	}
	return nil
}
