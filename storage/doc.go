// Package storage defines one streaming object-store contract for filesystem
// and MinIO backends.
//
// A user upload can be accepted before its surrounding form is committed:
//
//	staged, err := files.Stage(ctx, body, storage.StageOptions{
//		ContentType: "image/png",
//		ExpiresIn:   time.Hour,
//	})
//	// Send staged.ID.Value() back with the form, then after domain validation:
//	info, err := files.Promote(ctx, staged.ID, finalKey, storage.PromoteOptions{})
//
// Promotion defaults to CreateOnly. Abandoned uploads are removed by an
// application-owned, removal-bounded Store.CleanupExpired call made with the
// application's context/deadline; this package starts no background goroutine.
//
// Put never closes its source. Open always returns a body the caller must close.
// Keys, stage IDs and temporary URLs redact their String representation; use
// their explicit Value or URL method only at a persistence/response boundary.
package storage
