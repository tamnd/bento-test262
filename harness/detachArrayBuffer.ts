// TypeScript port of test262/harness/detachArrayBuffer.js. Upstream detaches the
// buffer through the host's $262.detachArrayBuffer hook, which bento has no runtime
// to provide. transfer() moves the bytes to a fresh buffer and leaves the receiver
// detached, the same observable end state the detach tests check, so it stands in
// for the host hook while keeping the $DETACHBUFFER name the tests call. The buffer
// is typed as an ArrayBuffer so the transfer lowers rather than routing through the
// dynamic method-call path an untyped receiver would take.
function $DETACHBUFFER(buffer: ArrayBuffer): void {
  buffer.transfer();
}
