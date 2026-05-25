"use strict";
// Wire-shape types for Talyvor Lens. We deliberately mirror the
// Anthropic Messages API request/response shape since Lens passes
// it through verbatim — keeping the type names familiar makes the
// proxy boundary read as a thin shim rather than a fresh protocol.
Object.defineProperty(exports, "__esModule", { value: true });
exports.LensError = void 0;
// LensError carries the user-visible message + the original status
// code so the extension can map specific failures (401 → "API key
// is invalid") without re-parsing strings.
class LensError extends Error {
    status;
    constructor(message, status) {
        super(message);
        this.status = status;
        this.name = "LensError";
    }
}
exports.LensError = LensError;
//# sourceMappingURL=types.js.map