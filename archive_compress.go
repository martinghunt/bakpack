package bakpack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/ulikunitz/xz"
)

func xzCompress(data []byte, opts BuildOptions) ([]byte, error) {
	threads := opts.XZThreads
	if threads <= 0 {
		threads = 1
	}
	cmd := exec.Command("xz", "-9e", fmt.Sprintf("-T%d", threads), "-c")
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("xz compression failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func xzDecompress(data []byte) ([]byte, error) {
	reader, err := xz.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(reader)
}

func isXZ(data []byte) bool {
	return len(data) >= 6 && bytes.Equal(data[:6], []byte{0xfd, '7', 'z', 'X', 'Z', 0x00})
}

func writeUvarint(w io.Writer, value uint64) {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], value)
	_, _ = w.Write(buf[:n])
}

func readUvarint(r io.ByteReader) (uint64, error) {
	return binary.ReadUvarint(r)
}

func writeString(w io.Writer, value string) {
	writeBytes(w, []byte(value))
}

func readString(r *bytes.Reader) (string, error) {
	data, err := readBytes(r)
	return string(data), err
}

func writeBytes(w io.Writer, data []byte) {
	writeUvarint(w, uint64(len(data)))
	_, _ = w.Write(data)
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
