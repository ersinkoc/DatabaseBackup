package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/kronos/kronos/internal/chunk"
	"github.com/kronos/kronos/internal/drivers"
	"github.com/kronos/kronos/internal/manifest"
)

// BackupResult is produced by a successful engine backup run.
type BackupResult struct {
	ResumePoint drivers.ResumePoint
	Chunks      []chunk.ChunkRef
	Stats       chunk.Stats
}

// BackupFull streams a driver's full backup output into the chunk pipeline.
func BackupFull(ctx context.Context, driver drivers.Driver, target drivers.Target, pipeline *chunk.Pipeline) (BackupResult, error) {
	if driver == nil {
		return BackupResult{}, fmt.Errorf("driver is required")
	}
	if pipeline == nil {
		return BackupResult{}, fmt.Errorf("pipeline is required")
	}
	reader, writer := io.Pipe()
	resultCh := make(chan driverResult, 1)
	go func() {
		recordWriter := &jsonRecordWriter{w: writer}
		rp, err := driver.BackupFull(ctx, target, recordWriter)
		if closeErr := writer.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		resultCh <- driverResult{resumePoint: rp, err: err}
	}()

	refs, stats, err := pipeline.Feed(ctx, reader)
	reader.Close()
	driverResult := <-resultCh
	if err != nil {
		return BackupResult{}, err
	}
	if driverResult.err != nil {
		return BackupResult{}, driverResult.err
	}
	return BackupResult{ResumePoint: driverResult.resumePoint, Chunks: refs, Stats: stats}, nil
}

// BackupIncremental streams a driver's incremental backup output into the chunk pipeline.
func BackupIncremental(ctx context.Context, driver drivers.Driver, target drivers.Target, parent manifest.Manifest, pipeline *chunk.Pipeline) (BackupResult, error) {
	if driver == nil {
		return BackupResult{}, fmt.Errorf("driver is required")
	}
	if pipeline == nil {
		return BackupResult{}, fmt.Errorf("pipeline is required")
	}
	reader, writer := io.Pipe()
	resultCh := make(chan driverResult, 1)
	go func() {
		recordWriter := &jsonRecordWriter{w: writer}
		rp, err := driver.BackupIncremental(ctx, target, parent, recordWriter)
		if closeErr := writer.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		resultCh <- driverResult{resumePoint: rp, err: err}
	}()

	refs, stats, err := pipeline.Feed(ctx, reader)
	reader.Close()
	driverResult := <-resultCh
	if err != nil {
		return BackupResult{}, err
	}
	if driverResult.err != nil {
		if errors.Is(driverResult.err, drivers.ErrIncrementalUnsupported) {
			return BackupFull(ctx, driver, target, pipeline)
		}
		return BackupResult{}, driverResult.err
	}
	return BackupResult{ResumePoint: driverResult.resumePoint, Chunks: refs, Stats: stats}, nil
}

// Restore reconstructs pipeline chunks and streams decoded records into driver.Restore.
func Restore(ctx context.Context, driver drivers.Driver, target drivers.Target, pipeline *chunk.Pipeline, refs []chunk.ChunkRef, opts drivers.RestoreOptions) (chunk.Stats, error) {
	if driver == nil {
		return chunk.Stats{}, fmt.Errorf("driver is required")
	}
	if pipeline == nil {
		return chunk.Stats{}, fmt.Errorf("pipeline is required")
	}
	reader, writer := io.Pipe()
	resultCh := make(chan error, 1)
	go func() {
		recordReader := &jsonRecordReader{decoder: json.NewDecoder(reader)}
		err := driver.Restore(ctx, target, recordReader, opts)
		reader.Close()
		resultCh <- err
	}()

	stats, err := pipeline.Restore(ctx, refs, writer)
	if closeErr := writer.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	driverErr := <-resultCh
	if err != nil {
		return stats, err
	}
	if driverErr != nil {
		return stats, driverErr
	}
	return stats, nil
}

type driverResult struct {
	resumePoint drivers.ResumePoint
	err         error
}

type jsonRecordWriter struct {
	w *io.PipeWriter
}

func (w *jsonRecordWriter) WriteRecord(obj drivers.ObjectRef, payload []byte) error {
	return w.write(drivers.Record{Object: obj, Payload: payload})
}

func (w *jsonRecordWriter) FinishObject(obj drivers.ObjectRef, rows int64) error {
	return w.write(drivers.Record{Object: obj, Rows: rows, Done: true})
}

func (w *jsonRecordWriter) write(record drivers.Record) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.w.Write(data)
	return err
}

type jsonRecordReader struct {
	decoder *json.Decoder
}

func (r *jsonRecordReader) NextRecord() (drivers.Record, error) {
	var record drivers.Record
	if err := r.decoder.Decode(&record); err != nil {
		return drivers.Record{}, err
	}
	return record, nil
}
