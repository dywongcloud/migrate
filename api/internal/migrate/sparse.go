package migrate

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

func seekConstants() (data, hole int) {
	if runtime.GOOS == "darwin" {
		return 4, 3
	}
	return 3, 4
}

func MergeDiffOntoBase(basePath, diffPath string) error {
	seekData, seekHole := seekConstants()
	diff, err := os.Open(diffPath)
	if err != nil {
		return err
	}
	defer diff.Close()
	base, err := os.OpenFile(basePath, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer base.Close()
	st, err := diff.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	baseSt, err := base.Stat()
	if err != nil {
		return err
	}
	if baseSt.Size() < size {
		if err := base.Truncate(size); err != nil {
			return err
		}
	}
	var offset int64
	buf := make([]byte, 1<<20)
	for offset < size {
		dataStart, err := diff.Seek(offset, seekData)
		if err != nil {
			break
		}
		dataEnd, err := diff.Seek(dataStart, seekHole)
		if err != nil {
			dataEnd = size
		}
		if _, err := diff.Seek(dataStart, io.SeekStart); err != nil {
			return err
		}
		remaining := dataEnd - dataStart
		writeAt := dataStart
		for remaining > 0 {
			n := int64(len(buf))
			if remaining < n {
				n = remaining
			}
			read, err := io.ReadFull(diff, buf[:n])
			if err != nil && err != io.ErrUnexpectedEOF {
				return fmt.Errorf("read diff extent at %d: %w", writeAt, err)
			}
			if read == 0 {
				break
			}
			if _, err := base.WriteAt(buf[:read], writeAt); err != nil {
				return fmt.Errorf("write base at %d: %w", writeAt, err)
			}
			writeAt += int64(read)
			remaining -= int64(read)
		}
		offset = dataEnd
	}
	return base.Sync()
}
