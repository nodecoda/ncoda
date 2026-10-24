package spec

import (
	"errors"
	"os"
)

func osReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

func errNoFile(name string) error { return errors.New("no corpus file: " + name) }
