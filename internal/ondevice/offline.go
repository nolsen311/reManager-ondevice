package ondevice

import "strings"

// OfflineMessage leads any error that looks like the tablet has no network. Airplane mode is easy to miss: on the Move, an app such as KOReader can turn Wi-Fi on by itself, and restarting the reMarkable UI turns it off again.
const OfflineMessage = "The tablet seems to be offline. Check that Wi-Fi is on and airplane mode is off in the reMarkable settings, then try again."

// What Go's HTTP client and apk print when there is no network or no DNS.
var offlineSigns = []string{
	"no such host",
	"network is unreachable",
	"temporary failure in name resolution",
	"dns",
	"network error",
	"i/o timeout",
	"no route to host",
}

func looksOffline(message string) bool {
	lower := strings.ToLower(message)
	for _, sign := range offlineSigns {
		if strings.Contains(lower, sign) {
			return true
		}
	}
	return false
}

// withOfflineHint puts OfflineMessage ahead of a message that looks like a network failure, keeping the original underneath.
func withOfflineHint(message string) string {
	if message == "" || strings.HasPrefix(message, OfflineMessage) || !looksOffline(message) {
		return message
	}
	return OfflineMessage + "\n\n" + message
}
