package filetransfer

import "path/filepath"

// CommitFile publishes a closed, synced temporary file on the same filesystem.
func CommitFile(source, destination string) error {
	if err := publishFile(source, destination); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}
