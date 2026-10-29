import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";

const file = new URL("../../delta/testdata/emitted-delta.json", import.meta.url);
const batches = JSON.parse(await readFile(file, "utf8"));
assert.ok(batches.length >= 8);

const RESERVED = new Set(["__proto__", "constructor", "prototype"]);

function assertSafePath(path) {
  for (const seg of path) {
    if (typeof seg === "string") {
      assert.ok(!RESERVED.has(seg), "reserved segment");
    } else {
      assert.ok(Number.isInteger(seg) && seg >= 0, "bad index");
    }
  }
}

function resolve(root, path) {
  let node = root;
  for (const seg of path) {
    assert.ok(node !== null && typeof node === "object", "unresolvable path");
    assert.ok(Object.hasOwn(node, seg), "missing own property");
    node = node[seg];
  }
  return node;
}

function decode(wire) {
  const paths = new Map();
  let previous;
  let hasPrevious = false;
  const out = [];
  for (const op of wire) {
    assert.ok(Array.isArray(op) && op.length > 0, "not a tuple");
    const verb = op[0];
    if (verb === "#") {
      assertSafePath(op[2]);
      paths.set(op[1], op[2]);
      continue;
    }
    if (verb === "r") {
      out.push(op);
      paths.clear();
      previous = undefined;
      hasPrevious = false;
      continue;
    }
    const short =
      (verb === "d" && op.length === 1) ||
      (verb !== "d" && verb !== "p" && op.length === 2) ||
      (verb === "p" && op.length === 4);
    let path;
    if (short) {
      assert.ok(hasPrevious, "short form without previous");
      path = previous;
    } else {
      const ref = op[1];
      path = typeof ref === "number" ? paths.get(ref) : ref;
      assert.ok(path !== undefined, "unresolved path id");
      assertSafePath(path);
      previous = path;
      hasPrevious = true;
    }
    switch (verb) {
      case "s":
        out.push(["s", path, short ? op[1] : op[2]]);
        break;
      case "d":
        out.push(["d", path]);
        break;
      case "a":
        out.push(["a", path, short ? op[1] : op[2]]);
        break;
      case "t":
        out.push(["t", path, short ? op[1] : op[2]]);
        break;
      case "p": {
        const [i, r, items] = short ? [op[1], op[2], op[3]] : [op[2], op[3], op[4]];
        out.push(["p", path, i, r, items]);
        break;
      }
      case "m":
        out.push(["m", path, short ? op[1] : op[2]]);
        break;
      default:
        assert.fail("unknown verb " + verb);
    }
  }
  return out;
}

function apply(root, ops) {
  for (const op of ops) {
    const verb = op[0];
    if (verb === "r") {
      root = op[1];
      continue;
    }
    const path = op[1];
    if (verb === "p") {
      const target = path.length === 0 ? root : resolve(root, path);
      assert.ok(Array.isArray(target), "splice target not array");
      target.splice(op[2], op[3], ...op[4]);
      continue;
    }
    if (verb === "m") {
      const target = path.length === 0 ? root : resolve(root, path);
      assert.ok(Array.isArray(target) && target.length === op[2].length, "permute mismatch");
      const previous = target.slice();
      for (let i = 0; i < op[2].length; i++) target[i] = previous[op[2][i]];
      continue;
    }
    const parent = resolve(root, path.slice(0, -1));
    const key = path[path.length - 1];
    switch (verb) {
      case "s":
        parent[key] = op[2];
        break;
      case "d":
        if (Array.isArray(parent)) parent.splice(key, 1);
        else delete parent[key];
        break;
      case "a":
        assert.equal(typeof parent[key], "string");
        parent[key] += op[2];
        break;
      case "t":
        assert.equal(typeof parent[key], "string");
        parent[key] = parent[key].slice(op[2]);
        break;
    }
  }
  return root;
}

for (const { before, wire, after } of batches) {
  const got = apply(before, decode(wire));
  assert.deepEqual(got, after);
}
console.log(`PASS ${batches.length} cross-language delta batches`);
