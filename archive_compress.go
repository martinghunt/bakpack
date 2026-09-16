package bakpack

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/ulikunitz/xz"
)

func xzCompress(ctx context.Context, data []byte, opts BuildOptions) ([]byte, error) {
	threads := opts.XZThreads
	if threads <= 0 {
		threads = 1
	}
	cmd := exec.CommandContext(ctx, "xz", "-9e", fmt.Sprintf("-T%d", threads), "-c")
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("xz compression failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// maxDecompressedComponentSize bounds the decompressed size of any single
// xz-compressed payload read from untrusted archive data (the index, a
// chunk's payload, or a tar.xz source entry). xz can achieve very high
// compression ratios, so bounding the compressed input size alone (see
// maxArchiveComponentSize) is not enough to stop a small malicious payload
// from expanding into an unbounded amount of memory during decompression.
var maxDecompressedComponentSize int64 = 4 << 30 // 4 GiB

// xzDecompress decompresses data, refusing to produce more than maxSize
// bytes of output so a compression bomb fails with a clean error instead of
// exhausting memory.
func xzDecompress(data []byte, maxSize int64) ([]byte, error) {
	reader, err := xz.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(reader, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > maxSize {
		return nil, fmt.Errorf("decompressed data exceeds %d byte limit", maxSize)
	}
	return out, nil
}

func isXZ(data []byte) bool {
	return len(data) >= 6 && bytes.Equal(data[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0x00})
}

// writeUvarint, writeString, and writeBytes take a *bytes.Buffer rather than
// a general io.Writer specifically because bytes.Buffer.Write is documented
// to never return an error, which is what lets them discard its result
// without silently swallowing a real write failure.
func writeUvarint(w *bytes.Buffer, value uint64) {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], value)
	w.Write(buf[:n])
}

func readUvarint(r io.ByteReader) (uint64, error) {
	return binary.ReadUvarint(r)
}

func writeString(w *bytes.Buffer, value string) {
	writeBytes(w, []byte(value))
}

func readString(r *bytes.Reader) (string, error) {
	data, err := readBytes(r)
	return string(data), err
}

func writeBytes(w *bytes.Buffer, data []byte) {
	writeUvarint(w, uint64(len(data)))
	w.Write(data)
}

func readBytes(r *bytes.Reader) ([]byte, error) {
	length, err := readUvarint(r)
	if err != nil {
		return nil, err
	}
	if length > uint64(r.Len()) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, length)
	_, err = io.ReadFull(r, out)
	return out, err
}
