package desktop

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func onechatTokenOwner(file *os.File, _ os.FileInfo) error {
	sd, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("verify one chat host-token ownership: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("verify one chat host-token owner: %w", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !owner.Equals(user.User.Sid) {
		return fmt.Errorf("one chat host-token must be owned by the current user; fix its ownership")
	}
	return nil
}
