import assert from "node:assert/strict";
import { readFileSync, readdirSync, existsSync } from "node:fs";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";

const root = new URL("../../build/interop", import.meta.url).pathname;

// ── SQLite schema parity ─────────────────────────────────────────────────────
const db = new DatabaseSync(join(root, "storage.sqlite"), { readOnly: true });
const tables = db
  .prepare("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
  .all()
  .map((r) => r.name);
const wantTables = [
  "conversations", "document_revisions", "documents", "durable_metadata",
  "durable_schema", "entries", "record_ids", "submissions", "tasks",
];
assert.deepEqual(tables, wantTables);
const indexes = db
  .prepare("SELECT name FROM sqlite_schema WHERE type='index' ORDER BY name")
  .all()
  .map((r) => r.name)
  .filter((n) => !n.startsWith("sqlite_"));
const wantIndexes = [
  "conversations_by_owner_conversation", "conversations_by_owner_task",
  "document_revisions_by_kind", "documents_by_address", "documents_by_scope",
  "documents_by_scope_kind", "entries_by_conversation", "entry_heads_by_conversation",
  "submissions_by_conversation", "submissions_by_request", "submissions_by_status",
  "tasks_by_abort_requested", "tasks_by_background", "tasks_by_conversation",
  "tasks_by_kind", "tasks_by_status",
];
assert.deepEqual(indexes, wantIndexes);
const row = db.prepare("SELECT record FROM conversations WHERE id = 1").get();
assert.equal(JSON.parse(row.record).id, 1);
db.close();

// ── JSONL format parity ──────────────────────────────────────────────────────
function lines(file) {
  if (!existsSync(file)) return [];
  return readFileSync(file, "utf8").trim().split("\n").filter(Boolean).map(JSON.parse);
}
function sidecarRecords(file) {
  const map = new Map();
  for (const rec of lines(file)) {
    assert.equal(rec.format, 1);
    assert.equal(rec.type, "record");
    map.set(`${rec.seq}:${rec.ordinal}`, rec.payload);
  }
  return map;
}
const dir = join(root, "jsonl");
const sidecars = new Map();
for (const name of readdirSync(dir)) {
  if (name.endsWith(".jsonl") && name !== "main.jsonl") {
    sidecars.set(name, sidecarRecords(join(dir, name)));
  }
}
let doc = undefined;
let docVersion = 0;
function applyOp(value, op) {
  const [verb, path, ...rest] = op;
  const get = (v, p) => p.reduce((n, s) => n[s], v);
  const parent = (p) => get(value, p.slice(0, -1));
  if (verb === "r") return op[1];
  const key = path[path.length - 1];
  if (verb === "s") { parent(path)[key] = op[2]; return value; }
  if (verb === "d") { if (Array.isArray(parent(path))) parent(path).splice(key, 1); else delete parent(path)[key]; return value; }
  if (verb === "a") { parent(path)[key] += op[2]; return value; }
  if (verb === "t") { parent(path)[key] = parent(path)[key].slice(op[2]); return value; }
  throw new Error("unexpected op " + verb);
}
for (const marker of lines(join(dir, "main.jsonl"))) {
  assert.equal(marker.format, 1);
  assert.equal(marker.type, "commit");
  for (const op of marker.writes) {
    if (op.type === "document.create" || op.type === "document.change") {
      const id = op.type === "document.create" ? op.record.id : op.id;
      const rec = sidecars.get(`doc-${id}.jsonl`).get(`${marker.seq}:${op.ordinal}`);
      assert.ok(rec, "document sidecar record");
      assert.equal(rec.id, id);
      if (rec.content.kind === "base") { doc = rec.content.value; docVersion = rec.content.version; }
      else {
        assert.equal(rec.content.version, docVersion);
        for (const deltaOp of rec.content.ops) doc = applyOp(doc, deltaOp);
      }
    }
  }
}
assert.deepEqual(doc, { v: 2 });
console.log("PASS SQLite schema and JSONL format interop");
