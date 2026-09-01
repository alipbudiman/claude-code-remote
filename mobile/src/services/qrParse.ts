/**
 * M8: pure mapping from a scanned QR string to connection-form values.
 * Kept free of DOM/React deps so it is unit-testable from plain Node.
 */

export type ScannedQrResult =
  | {
      /** An http(s) URL: save `base` into the field named by `targetField`. */
      kind: 'url';
      /** Normalized origin + path (no query/hash, no trailing slash) — the
       * same normalization `wsService.setServerUrl` applies. */
      base: string;
      /** Value of the `token` query param, '' when the URL carries none. */
      token: string;
      /** Scheme decides the field: http → LAN, https → Railway. */
      targetField: 'lan' | 'railway';
    }
  | {
      /** Not a URL (raw token / plain text): fill the token field only.
       * There is no URL to connect to, so nothing may be auto-saved. */
      kind: 'text';
      token: string;
    };

/**
 * The server generates a 64-hex-char token (`internal/network` QR URL +
 * `~/.claude/claude-remote-token`). Used purely for scan feedback — the
 * save path never rejects a manually typed token.
 */
export const looksLikeToken = (raw: string): boolean =>
  /^[0-9a-f]{64}$/i.test(raw.trim());

export const parseScannedQR = (raw: string): ScannedQrResult => {
  const s = (raw || '').trim();
  if (/^https?:\/\//i.test(s)) {
    try {
      const parsed = new URL(s);
      if (parsed.hostname) {
        const token = (parsed.searchParams.get('token') || '').trim();
        const base = (parsed.origin + parsed.pathname).replace(/\/+$/, '');
        return {
          kind: 'url',
          base,
          token,
          targetField: parsed.protocol === 'https:' ? 'railway' : 'lan',
        };
      }
    } catch {
      // Unparseable despite the scheme: treat as plain text below.
    }
  }
  return { kind: 'text', token: s };
};
