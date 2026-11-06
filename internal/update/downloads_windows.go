package update

import (
	"os"

	"golang.org/x/sys/windows"
)

// DownloadsDir is the user's Downloads folder as Windows knows it (it may have been moved),
// else %USERPROFILE%\Downloads, else the profile folder.
func DownloadsDir() (string, error) {
	if d, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, 0); err == nil && d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return downloadsIn(home), nil
}
