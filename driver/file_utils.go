package driver

import (
	"os"
)

func dirExists(dirname string) bool {
	info, err := os.Stat(dirname)
	return err == nil && info.IsDir()
}

func fileExists(dirname string) bool {
	info, err := os.Stat(dirname)
	return err == nil && !info.IsDir()
}
