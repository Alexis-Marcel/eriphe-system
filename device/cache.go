package main

// The tasks' /cache keeps what they download (packages, modules, toolchain
// builds) for the next ones, which run it. A task eriphe-core started on
// what came from outside could leave something there for a trusted task to
// run: so the two never share one. The untrusted tasks get a cache of their
// own, next to the other.

// cacheFor is the cache (a volume, or a folder of the root) of a task: the
// one given, or its untrusted twin.
func cacheFor(cache string, untrusted bool) string {
	if untrusted {
		return cache + "-untrusted"
	}
	return cache
}
