package batch

// Merge returns the effective matcher set for one record: defaults overlaid
// per key by record, with record winning on conflict.
//
// Neither input is modified. A record can override an inherited default but
// cannot remove one; there is deliberately no v1 encoding for "apply every
// batch-wide matcher except this one". A caller needing that should pass only
// the genuinely universal matchers on the command line and carry the rest per
// record.
func Merge(defaults, record map[string]string) map[string]string {
	merged := make(map[string]string, len(defaults)+len(record))
	for name, value := range defaults {
		merged[name] = value
	}
	for name, value := range record {
		merged[name] = value
	}
	return merged
}
