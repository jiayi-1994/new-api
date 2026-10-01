// Offline contract tests; never submits a request to Paipu.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import * as plugin from "./plugin.js";

globalThis.utils = { unixNow: () => Math.floor(Date.now() / 1000) };
const fixture = JSON.parse(await readFile(new URL("./fixture.json", import.meta.url), "utf8"));
const catalog = JSON.parse(await readFile(new URL("./model-catalog.json", import.meta.url), "utf8"));
const registered = catalog.models.filter(model => model.status === "registered");
assert.equal(registered.length, 39);
assert.deepEqual(plugin.meta.models.filter(name => !name.startsWith("paipu-video")).sort(), registered.map(model => model.model).sort());
for (const model of registered) {
  const profile = plugin.meta.usageProfiles.find(profile => profile.models.includes(model.model));
  assert.ok(profile, `missing pricing schema for ${model.model}`);
  assert.deepEqual(profile.schema.resolution?.enum ?? [], model.resolutions, model.model);
  assert.equal(profile.schema.requests.unit, "count");
  assert.equal(profile.schema.seconds.unit, "second");
  for (const example of profile.examples) {
    assert.deepEqual(Object.keys(example.facts).sort(), Object.keys(profile.schema).sort());
    if (model.fixed_duration) assert.equal(example.facts.seconds, model.fixed_duration);
  }
}
for (const model of catalog.models.filter(model => model.status !== "registered")) {
  assert.ok(!plugin.meta.models.includes(model.model), `${model.model} must not be advertised as a supported video model`);
}
// describeSpec: the spec a submission is scheduled by agrees with the body sent
// upstream and with reserved usage, and rejects exactly what usage rejects.
for (const test of fixture.cases.filter(test => (test.path || []).includes("decodeRequest") && test.expected?.requestBody)) {
  const ctx = { model: test.args[0].model, upstreamModel: test.args[0].upstreamModel || test.args[0].model, requestBody: test.expected.requestBody };
  let usage;
  try { usage = plugin.extractUsage(ctx); } catch (error) {
    assert.throws(() => plugin.describeSpec(ctx), { message: error.message }, test.name);
    continue;
  }
  const spec = plugin.describeSpec(ctx); // no credentials: the hook is read-only
  const body = plugin.buildSubmitRequest({ ...ctx, baseUrl: "https://api.paipu.net", apiKey: "fixture-only-key" }).body;
  assert.deepEqual(spec, {
    spec_version: 1, output_seconds: usage.seconds,
    seconds_kind: Object.hasOwn(body, "duration") ? "exact" : "fixed", // fixed-length models omit duration upstream
    resolution: usage.resolution || "*",
    references: { video: (body.videos || []).length, image: (body.images || []).length, audio: (body.audios || []).length },
  }, test.name);
}
{
  const describe = (model, upstreamModel, value) => {
    const intent = plugin.protocols.openai_video.decodeRequest({ model, upstreamModel, body: { kind: "json", value: { model, prompt: "cat", ...value } } });
    return plugin.describeSpec({ model, upstreamModel, requestBody: intent.requestBody });
  };
  assert.deepEqual(describe("lec-gt-seedance-2-0-full", "lec-gt-seedance-2-0-full", {
    duration: 5, resolution: "720p",
    images: ["https://cdn.example/1.png", "https://cdn.example/2.png"], videos: ["https://cdn.example/1.mp4"], audios: ["https://cdn.example/1.mp3"],
  }), { spec_version: 1, output_seconds: 5, seconds_kind: "exact", resolution: "720p", references: { video: 1, image: 2, audio: 1 } });
  const none = { video: 0, image: 0, audio: 0 };
  assert.deepEqual(describe("lec-bk-video-30s", "lec-bk-video-30s", {}),
    { spec_version: 1, output_seconds: 30, seconds_kind: "fixed", resolution: "720p", references: none });
  assert.deepEqual(describe("lec-seed-2-0-900", "lec-seed-2-0-900", {}),
    { spec_version: 1, output_seconds: 15, seconds_kind: "fixed", resolution: "*", references: none }, "no published tier is untiered, not unknown");
  assert.equal(describe("lec-seedance-2-0", "lec-seedance-2-0", { duration: 5 }).resolution, "720p", "a single published tier is the default");
}
console.log("describeSpec contract checks passed");

// Provider-side cancellations and constraint violations count against the upstream.
for (const [reason, kind] of [
  ["job cancelled by provider due to internal error", "upstream"],
  ["constraint violation", "upstream"],
  ["content policy violation", "user"],
]) assert.equal(plugin.classifyFailure(reason), kind, reason);

let failed = 0;
for (const test of fixture.cases) {
  let hook = plugin[test.hook];
  for (const name of test.path || []) hook = hook[name];
  try {
    if (test.expectedError) {
      assert.throws(() => hook(...test.args), (error) => {
        assert.ok(error.message.includes(test.expectedError), error.message);
        return true;
      });
    } else {
      assert.deepEqual(hook(...test.args), test.expected);
    }
  } catch (error) {
    failed++;
    console.error("FAIL:", test.name, error);
  }
}
console.log(`${fixture.cases.length - failed}/${fixture.cases.length} offline contract cases passed`);
process.exitCode = failed ? 1 : 0;
