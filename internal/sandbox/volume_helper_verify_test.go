package sandbox

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestHelperArchiveHashesBytesAndRejectsLinksAndExtraEntries(t *testing.T) {
	archive := func(link, extra bool) []byte {
		var buffer bytes.Buffer
		writer := tar.NewWriter(&buffer)
		header := &tar.Header{Name: "point-sandboxd", Mode: 0555, Size: 6, Typeflag: tar.TypeReg}
		if link {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = "other"
			header.Size = 0
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if !link {
			_, _ = writer.Write([]byte("binary"))
		}
		if extra {
			_ = writer.WriteHeader(&tar.Header{Name: "extra", Typeflag: tar.TypeReg})
		}
		_ = writer.Close()
		return buffer.Bytes()
	}
	digest, err := helperArchiveDigest(archive(false, false))
	if err != nil || digest != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("binary"))) {
		t.Fatalf("digest=%q err=%v", digest, err)
	}
	for _, input := range [][]byte{archive(true, false), archive(false, true), []byte("corrupt")} {
		if _, err := helperArchiveDigest(input); err == nil {
			t.Fatal("accepted untrusted helper archive")
		}
	}
}
