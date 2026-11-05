//go:build !windows

package clusterconfig

import "os"

func privateDrainFile(_ string, st os.FileInfo) bool { return st.Mode().Perm()&0077 == 0 }
func protectDrainFile(path string) error             { return os.Chmod(path, 0600) }
