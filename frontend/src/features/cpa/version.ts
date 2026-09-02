export function versionBelow(version: string, minimum: string) {
  const parse = (value: string) =>
    value
      .replace(/^v/, '')
      .split(/[+-]/)[0]
      .split('.')
      .map((part) => Number(part) || 0);
  const left = parse(version);
  const right = parse(minimum);
  for (let index = 0; index < Math.max(left.length, right.length); index += 1) {
    if ((left[index] ?? 0) < (right[index] ?? 0)) return true;
    if ((left[index] ?? 0) > (right[index] ?? 0)) return false;
  }
  return false;
}
