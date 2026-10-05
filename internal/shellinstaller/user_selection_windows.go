//go:build windows

package shellinstaller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

func userConfirmation(req UserInstallRequest, identity string) string {
	data, _ := json.Marshal([]string{req.Destination, req.Mode, req.SharedPrefix, req.SharedAgent, identity})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func UserInstallFromEntry(args []string) (UserInstallRequest, error) {
	if len(args) != 5 {
		return UserInstallRequest{}, fmt.Errorf("invalid internal install arguments")
	}
	return UserInstallRequest{args[0], args[1], args[2], args[3], args[4]}, nil
}
