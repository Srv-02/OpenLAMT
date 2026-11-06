//go:build !windows

package update

import "os"

// DownloadsDir is ~/Downloads, or the home folder when there is none.
func DownloadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return downloadsIn(home), nil
}
