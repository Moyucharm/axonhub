export function isExternalHttpURL(raw: string): boolean {
  try {
    const value = raw.trim();
    if (!value) return false;
    const url = new URL(/^[a-z][a-z\d+.-]*:\/\//i.test(value) ? value : `http://${value}`);
    if (url.protocol !== 'http:') return false;

    const hostname = url.hostname.toLowerCase().replace(/\.$/, '');
    if (
      hostname === 'localhost' ||
      hostname === 'host.docker.internal' ||
      hostname.endsWith('.local') ||
      hostname.endsWith('.internal') ||
      !hostname.includes('.')
    ) {
      return false;
    }

    const parts = hostname.split('.').map(Number);
    if (parts.length === 4 && parts.every((part) => Number.isInteger(part) && part >= 0 && part <= 255)) {
      const [first, second] = parts;
      const isPrivate = first === 10 || (first === 172 && second >= 16 && second <= 31) || (first === 192 && second === 168);
      const isLocal = first === 127 || first === 0 || (first === 169 && second === 254);
      return !isPrivate && !isLocal;
    }
    return true;
  } catch {
    return false;
  }
}
