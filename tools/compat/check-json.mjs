import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const file = new URL("../../testdata/json-corpus.json", import.meta.url);
const cases = JSON.parse(await readFile(file, "utf8"));
assert.equal(cases.length, 13);

function finite(value) {
  if (typeof value === "number") assert.ok(Number.isFinite(value));
  if (value !== null && typeof value === "object") {
    for (const child of Object.values(value)) finite(child);
  }
}

for (const test of cases) {
  let value;
  let valid = true;
  try {
    value = JSON.parse(test.raw);
    finite(value);
  } catch {
    valid = false;
  }
  assert.equal(valid, test.valid, test.name);
  if (!valid) continue;
  const kind = value === null ? "null" : Array.isArray(value) ? "array" : typeof value;
  assert.equal(kind, test.kind, test.name);
  if (Object.hasOwn(test, "units")) {
    const units = Array.from({ length: value.length }, (_, i) => value.charCodeAt(i));
    assert.deepEqual(units, test.units, test.name);
  }
  if (Object.hasOwn(test, "number")) {
    assert.ok(value === test.number, test.name);
    if (value === 0) assert.equal(Object.is(value, -0), test.negativeZero ?? false, test.name);
  }
}
console.log(`PASS ${cases.length} JavaScript value cases`);
