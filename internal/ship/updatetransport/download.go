package updatetransport

import (
	"context"
	"errors"
	"io"
)

func copyPackage(ctx context.Context, dst io.Writer, source io.Reader, size int64, progress func(int64, int64)) error {
	sink := &packageWriter{dst: dst, remaining: size, size: size, progress: progress}
	bounded := io.LimitReader(source, size+1)
	buffer := make([]byte, 64<<10)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, readErr := bounded.Read(buffer)

		if err := ctx.Err(); err != nil {
			return err
		}

		if n > 0 {
			written, err := sink.Write(buffer[:n])
			if err != nil {
				return err
			}

			if written != n {
				return io.ErrShortWrite
			}
		}

		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return readErr
			}

			break
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if sink.remaining != 0 {
		return io.ErrUnexpectedEOF
	}

	return nil
}

type packageWriter struct {
	dst       io.Writer
	remaining int64
	size      int64
	progress  func(int64, int64)
}

func (w *packageWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errors.New("package exceeds its declared size")
	}

	n, err := w.dst.Write(data)

	w.remaining -= int64(n)
	if n > 0 && w.progress != nil {
		w.progress(w.size-w.remaining, w.size)
	}

	return n, err
}
