//go:build !unix

package updatetransport

// checkPrivateAncestry has no ownership model to check here; the cache is
// protected by its own access control list.
func checkPrivateAncestry(string) error { return nil }
