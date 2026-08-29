# ADR-001: Why Go for the data plane

Status: Accepted

Go provides predictable concurrency, goroutines, low memory overhead,
excellent stdlib HTTP, and a strong race detector. The data plane must
survive traffic spikes with minimal overhead.

Alternatives considered: Python and Node were rejected for the data plane
due to concurrency model and latency characteristics.