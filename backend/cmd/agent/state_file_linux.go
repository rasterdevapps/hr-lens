package main

import "os"

func replaceStateFile(src, dst string) error { return os.Rename(src, dst) }
