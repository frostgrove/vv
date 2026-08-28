// Package storageminio implements storage.Backend with a caller-owned MinIO
// Go client. Client construction, credentials, endpoint selection, retries and
// transport lifetime remain application concerns.
//
// Logical objects are stored below <prefix>/<namespace>. Unconfirmed uploads
// use a private sibling prefix and carry reserved metadata used only for
// promotion and bounded cleanup. Reserved metadata is never returned through
// storage.Info.
//
// CreateOnly uses a conditional single PUT and is therefore limited to
// MaxCreateOnlySize. Unknown-size CreateOnly input is first streamed to a
// private stage so its size is known before final conditional placement.
package storageminio
