package sandbox

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// Managed runtime builds have exactly one trusted Dockerfile. Stream it into
// Linux; never enable DrvFS or mount a Windows build directory in the guest.
func runtimeBuildContext(args []string) ([]string, *bytes.Reader, error) {
	if len(args) == 0 || args[0] != "build" {
		return args, nil, nil
	}
	index := -1
	for i := 1; i < len(args)-1; i++ {
		if args[i] == "--file" {
			index = i + 1
			break
		}
	}
	if index < 0 || len(args) < 4 {
		return nil, nil, errors.New("embedded managed build requires a Dockerfile")
	}
	root, err := filepath.Abs(args[len(args)-1])
	if err != nil {
		return nil, nil, err
	}
	file, err := filepath.Abs(args[index])
	if err != nil {
		return nil, nil, err
	}
	if filepath.Dir(file) != root {
		return nil, nil, errors.New("managed Dockerfile escapes its build root")
	}
	info, err := os.Lstat(file)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return nil, nil, errors.New("managed Dockerfile is not a bounded regular file")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, nil, err
	}
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)
	if err = tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return nil, nil, err
	}
	if _, err = tw.Write(data); err != nil {
		return nil, nil, err
	}
	if err = tw.Close(); err != nil {
		return nil, nil, err
	}
	result := append([]string(nil), args...)
	result[index] = "Dockerfile"
	result[len(result)-1] = "-"
	return result, bytes.NewReader(buffer.Bytes()), nil
}
