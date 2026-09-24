package config

// Windows has no way to sync a directory handle, and NTFS journals the rename.
func syncDir(string) error { return nil }
