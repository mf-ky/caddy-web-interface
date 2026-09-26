//go:build !unix

package store

func matchOwner(file, dir string) {}
