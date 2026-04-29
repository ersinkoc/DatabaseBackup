package verify

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"

	"github.com/kronos/kronos/internal/chunk"
	"github.com/kronos/kronos/internal/manifest"
	"github.com/kronos/kronos/internal/storage"
)

// ManifestReport summarizes a manifest-level verification run.
type ManifestReport struct {
	Objects       int
	Chunks        int
	MissingChunks []string
	StoredBytes   int64
}

// ChunkReport summarizes full chunk-integrity verification.
type ChunkReport struct {
	ManifestReport
	VerifiedChunks int
	RestoredBytes  int64
}

// Manifest checks the manifest signature and verifies every referenced chunk exists.
func Manifest(ctx context.Context, backend storage.Backend, m manifest.Manifest, publicKey ed25519.PublicKey) (ManifestReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if backend == nil {
		return ManifestReport{}, fmt.Errorf("storage backend is required")
	}
	if err := m.Verify(publicKey); err != nil {
		return ManifestReport{}, err
	}

	report := ManifestReport{Objects: len(m.Objects)}
	for _, object := range m.Objects {
		for _, ref := range object.Chunks {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			report.Chunks++
			info, err := backend.Head(ctx, ref.Key)
			if err != nil {
				report.MissingChunks = append(report.MissingChunks, ref.Key)
				continue
			}
			report.StoredBytes += info.Size
		}
	}
	if len(report.MissingChunks) > 0 {
		return report, fmt.Errorf("manifest references %d missing chunks: %s", len(report.MissingChunks), summarizeChunkKeys(report.MissingChunks))
	}
	return report, nil
}

// Chunks verifies the manifest, then restores every referenced object to a sink.
func Chunks(ctx context.Context, pipeline *chunk.Pipeline, m manifest.Manifest, publicKey ed25519.PublicKey) (ChunkReport, error) {
	if pipeline == nil {
		return ChunkReport{}, fmt.Errorf("pipeline is required")
	}
	report, err := Manifest(ctx, pipeline.Backend, m, publicKey)
	chunkReport := ChunkReport{ManifestReport: report}
	if err != nil {
		return chunkReport, err
	}
	for _, object := range m.Objects {
		refs, err := manifest.PipelineRefs(object.Chunks)
		if err != nil {
			return chunkReport, err
		}
		stats, err := pipeline.Restore(ctx, refs, io.Discard)
		if err != nil {
			return chunkReport, fmt.Errorf("verify object %q chunks: %w", object.Name, err)
		}
		chunkReport.VerifiedChunks += stats.Chunks
		chunkReport.RestoredBytes += stats.BytesIn
	}
	return chunkReport, nil
}

func summarizeChunkKeys(keys []string) string {
	const maxKeys = 3
	if len(keys) <= maxKeys {
		return fmt.Sprint(keys)
	}
	return fmt.Sprintf("%v and %d more", keys[:maxKeys], len(keys)-maxKeys)
}
