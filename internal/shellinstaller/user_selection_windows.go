//go:build windows

package shellinstaller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

func userConfirmation(req UserInstallRequest, identity string) string {
	data, _ := json.Marshal([]string{req.Destination, req.Mode, req.SharedPrefix, req.SharedAgent, req.Channel, identity})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func UserInstallFromEntry(args []string) (UserInstallRequest, error) {
	if len(args) != 5 && (len(args) != 7 || args[5] != "--channel") {
		return UserInstallRequest{}, fmt.Errorf("invalid internal install arguments")
	}
	channel := "stable"
	if len(args) == 7 {
		channel = args[6]
		if channel == "" {
			return UserInstallRequest{}, fmt.Errorf("missing internal install channel")
		}
	}
	channel, err := UserInstallChannel(channel)
	if err != nil {
		return UserInstallRequest{}, err
	}
	return UserInstallRequest{Destination: args[0], Mode: args[1], SharedPrefix: args[2], SharedAgent: args[3], Confirmation: args[4], Channel: channel}, nil
}
