import { describe, expect, it } from 'vitest';
import { isExternalHttpURL } from './url';

describe('isExternalHttpURL', () => {
  it('warns for external HTTP hosts', () => {
    expect(isExternalHttpURL('http://example.com')).toBe(true);
    expect(isExternalHttpURL('http://8.8.8.8:8317')).toBe(true);
  });

  it('does not warn for HTTPS or private Docker-compatible addresses', () => {
    expect(isExternalHttpURL('https://example.com')).toBe(false);
    expect(isExternalHttpURL('http://localhost:8317')).toBe(false);
    expect(isExternalHttpURL('http://cpa:8317')).toBe(false);
    expect(isExternalHttpURL('http://172.20.0.4:8317')).toBe(false);
    expect(isExternalHttpURL('http://host.docker.internal:8317')).toBe(false);
  });
});
