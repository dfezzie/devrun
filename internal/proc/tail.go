package proc

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"
)

// TailFile writes the last lines lines of path (all if lines<=0) to out and,
// if follow, keeps streaming appended data until ctx is cancelled.
func TailFile(ctx context.Context, path string, lines int, follow bool, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if _, err := out.Write(lastLines(data, lines)); err != nil {
		return err
	}
	if !follow {
		return nil
	}
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
			continue
		}
		if err != nil && err != io.EOF {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func lastLines(data []byte, n int) []byte {
	if n <= 0 {
		return data
	}
	trimmed := bytes.TrimRight(data, "\n")
	idx := len(trimmed)
	for i := 0; i < n; i++ {
		j := bytes.LastIndexByte(trimmed[:idx], '\n')
		if j < 0 {
			return data
		}
		idx = j
	}
	return data[idx+1:]
}
