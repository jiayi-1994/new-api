// Contract: ak2api app/model_catalog.py, app/main.py, app/service.py and
// app/akool_client.py, checked 2026-10-17. These are AK2API model IDs, not
// AKOOL's internal /image-to-video or /reference-to-video identifiers.
const MODEL_SPECS = {
  "doubao-seedance-2-0-mini-260615": { maxSeconds: 15, tiers: ["480p", "720p"], images: 9, videos: 3, audios: 3, search: true },
  "doubao-seedance-2-0-fast-260128": { maxSeconds: 15, tiers: ["480p", "720p"], images: 9, videos: 3, audios: 3, search: true },
  "doubao-seedance-2-0-260128": { maxSeconds: 15, tiers: ["480p", "720p", "1080p", "4k"], images: 9, videos: 3, audios: 3, search: true },
  "doubao-seedance-2-5": { maxSeconds: 30, tiers: ["480p", "720p", "1080p"], images: 30, videos: 10, audios: 10 },
  "wan-3.0": { maxSeconds: 30, tiers: ["480p", "720p", "1080p"], images: 9, videos: 3, audios: 3 },
  "minimax-h3": { maxSeconds: 15, tiers: ["768p", "2k"], images: 9, videos: 3, audios: 3 },
};
const ALIASES = {
  "ak2api-seedance-2.0-mini": "doubao-seedance-2-0-mini-260615",
  "ak2api-seedance-2.0-fast": "doubao-seedance-2-0-fast-260128",
  "ak2api-seedance-2.0": "doubao-seedance-2-0-260128",
  "ak2api-seedance-2.5": "doubao-seedance-2-5",
  "ak2api-wan-3.0": "wan-3.0",
  "ak2api-minimax-h3": "minimax-h3",
  "seedance-2.0-mini": "doubao-seedance-2-0-mini-260615",
  "seedance-2.0-fast": "doubao-seedance-2-0-fast-260128",
  "sd-2-0-fast": "doubao-seedance-2-0-fast-260128",
  "seedance-2.0": "doubao-seedance-2-0-260128",
  "sd-2-0": "doubao-seedance-2-0-260128",
  "seedance-2.5": "doubao-seedance-2-5",
  "wan-3.0": "wan-3.0",
  "wan3.0": "wan-3.0",
  "alibaba/wan-3.0": "wan-3.0",
  "alibaba/wan-3.0/image-to-video": "wan-3.0",
  "minimax/h3": "minimax-h3",
  "minimax/h3/reference-to-video": "minimax-h3",
};
const RATIOS = ["adaptive", "16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
const TIERS = ["480p", "720p", "768p", "1080p", "2k", "4k"];
const MEDIA = [
  { names: ["images", "image_urls"], limit: 30 },
  { names: ["videos", "video_urls"], limit: 10 },
  { names: ["audios", "audio_urls"], limit: 10 },
];
const MEDIA_FIELDS = MEDIA.flatMap(function (group) { return group.names; });
const BOOLEAN_FIELDS = ["generate_audio", "web_search", "all_in_one_reference", "video_extend"];
const MIRROR_FIELDS = ["seconds", "duration", "resolution", "video_resolution", "size", "video_size", "ratio", "aspect_ratio", "aspectRatio", "generate_audio", "content"];
const FIELDS = ["model", "prompt", "n", "metadata", "negative_prompt"].concat(MIRROR_FIELDS, MEDIA_FIELDS, BOOLEAN_FIELDS);

export const meta = {
  apiVersion: 1,
  key: "ak2api",
  name: "AK2API",
  version: "1.0.0",
  author: { name: "jiayi-1994" },
  icon: "text:AK",
  description: { en: "Seedance, Wan and Minimax video generation through AK2API", zh: "通过 AK2API 生成 Seedance、Wan 和 Minimax 视频" },
  baseUrl: "http://82.158.230.113:8795",
  // Keep official Ark/MiniMax identities and their token pricing out of this
  // seconds-priced relay pool. Native IDs remain valid model_mapping targets.
  models: Object.keys(ALIASES),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  // No reliable output usage is returned by the public task endpoint.
  // Reserve validated requested seconds; administrators supply all prices.
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    resolution: { enum: TIERS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
    video_input: { type: "boolean", description: { en: "Reference video present", zh: "存在参考视频" } },
    generate_audio: { type: "boolean", description: { en: "Whether audio is generated", zh: "是否生成音频" } },
  },
  usageExamples: [{ label: "480p · 4s", facts: { seconds: 4, resolution: "480p", video_input: false, generate_audio: true } }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value) {
  return typeof value === "string" && value.length <= 8192 && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value) && !/[\u0000-\u001f\u007f]/.test(value);
}

function duration(value) {
  // Stricter than relay/common.MaxTaskDurationSeconds; final model caps apply below.
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+$/.test(String(value))) throw new Error("duration must be an integer between 4 and 30 seconds");
  const seconds = Number(value);
  if (!Number.isInteger(seconds) || seconds < 4 || seconds > 30) throw new Error("duration must be an integer between 4 and 30 seconds");
  return seconds;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be a supported video tier");
  const aliases = { "2160p": "4k" };
  const lower = value.toLowerCase();
  const tier = has(aliases, lower) ? aliases[lower] : lower;
  if (!TIERS.includes(tier)) throw new Error("unsupported video resolution: " + value);
  return tier;
}

function ratio(value) {
  if (!RATIOS.includes(value)) throw new Error("unsupported aspect ratio: " + String(value));
  return value;
}

function boolean(value, field) {
  if (typeof value !== "boolean") throw new Error(field + " must be a boolean");
  return value;
}

function scalarAlias(input, metadata, names, parse) {
  let selected;
  for (const source of [input, metadata]) {
    for (const name of names) {
      if (!has(source, name)) continue;
      const value = parse(source[name], name);
      if (selected !== undefined && selected !== value) throw new Error("conflicting values for " + names.join("/"));
      selected = value;
    }
  }
  return selected;
}

// One immutable normalization chain for decoding, billing, scheduling and
// submission. Mirrors must agree; unknown fields must never vanish silently.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const name of Object.keys(input)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  if (has(input, "n") && input.n !== 1) throw new Error("n must be 1");
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  if (Array.from(input.prompt.trim()).length > 20000) throw new Error("prompt exceeds 20000 characters");
  const metadata = has(input, "metadata") ? input.metadata : {};
  if (!isObject(metadata)) throw new Error("metadata must be an object");
  for (const name of Object.keys(metadata)) {
    if (!MIRROR_FIELDS.includes(name)) throw new Error("unsupported metadata parameter: " + name);
  }
  const seconds = scalarAlias(input, metadata, ["seconds", "duration"], duration);
  let tier = scalarAlias(input, metadata, ["resolution", "video_resolution"], resolution);
  let aspect = scalarAlias(input, metadata, ["ratio", "aspect_ratio", "aspectRatio"], ratio);
  for (const source of [input, metadata]) {
    for (const name of ["size", "video_size"]) {
      if (!has(source, name)) continue;
      if (typeof source[name] !== "string") throw new Error("size must be a resolution tier or WIDTHxHEIGHT");
      const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(source[name]);
      let sizeTier;
      if (match) {
        const width = Number(match[1]), height = Number(match[2]);
        sizeTier = resolution(Math.min(width, height) + "p");
        const sizeRatio = RATIOS.find(function (value) {
          const parts = value.split(":");
          return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01;
        });
        if (!sizeRatio) throw new Error("size has an unsupported aspect ratio");
        if (aspect !== undefined && aspect !== sizeRatio) throw new Error("size conflicts with ratio");
        aspect = sizeRatio;
      } else sizeTier = resolution(source[name]);
      if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
      tier = sizeTier;
    }
  }
  const request = { prompt: input.prompt.trim(), duration: seconds === undefined ? 4 : seconds, aspect_ratio: aspect || "adaptive" };
  // Resolve model-dependent defaults only after channel model_mapping.
  if (tier !== undefined) request.resolution = tier;
  for (const name of BOOLEAN_FIELDS) {
    const value = scalarAlias(input, metadata, [name], boolean);
    if (value !== undefined) request[name] = value;
  }
  if (has(input, "negative_prompt")) {
    if (typeof input.negative_prompt !== "string" || Array.from(input.negative_prompt).length > 20000) throw new Error("negative_prompt must be a string of at most 20000 characters");
    request.negative_prompt = input.negative_prompt;
  }
  const contentMirrors = [];
  for (const source of [input, metadata]) {
    if (!has(source, "content")) continue;
    if (!Array.isArray(source.content) || source.content.length > 50) throw new Error("content must be an array of at most 50 references");
    const references = {};
    for (const item of source.content) {
      if (!isObject(item) || !["image_url", "video_url", "audio_url"].includes(item.type)) throw new Error("content supports image_url, video_url or audio_url references");
      const kind = item.type.slice(0, -4);
      if (has(item, "role") && item.role !== "reference_" + kind) throw new Error("unsupported content role: " + item.role);
      for (const name of Object.keys(item)) {
        if (!["type", "role", item.type].includes(name)) throw new Error("unsupported content parameter: " + name);
      }
      const media = item[item.type];
      if (!isObject(media) || Object.keys(media).length !== 1 || !httpURL(media.url)) throw new Error("content reference must contain an HTTP(S) url without credentials");
      const field = kind + "s";
      if (!has(references, field)) references[field] = [];
      references[field].push(media.url);
    }
    contentMirrors.push(references);
  }
  for (const group of MEDIA) {
    let selected;
    for (const source of [input].concat(contentMirrors)) {
      for (const name of group.names) {
        if (!has(source, name)) continue;
        const urls = source[name];
        if (!Array.isArray(urls) || urls.length > group.limit || urls.some(function (url) { return !httpURL(url); })) throw new Error(name + " must be an array of at most " + group.limit + " HTTP(S) URLs without credentials");
        if (selected !== undefined && JSON.stringify(selected) !== JSON.stringify(urls)) throw new Error("conflicting reference fields: " + group.names.join("/"));
        selected = Array.from(urls);
      }
    }
    // AK2API explicitly deduplicates by URL in model_catalog._deduplicate.
    // Submit the same unique ordered list used for procurement/reference usage.
    if (selected && selected.length) request[group.names[1]] = Array.from(new Set(selected));
  }
  return request;
}

function modelRequest(ctx) {
  const selected = ctx.upstreamModel || ctx.model;
  const model = has(ALIASES, selected) ? ALIASES[selected] : selected;
  if (!has(MODEL_SPECS, model)) throw new Error("unsupported AK2API model: " + String(selected));
  const spec = MODEL_SPECS[model];
  const request = videoRequest(ctx.requestBody);
  if (request.duration > spec.maxSeconds) throw new Error(model + " supports duration from 4 to " + spec.maxSeconds + " seconds");
  const tier = request.resolution === undefined ? spec.tiers[0] : request.resolution;
  if (!spec.tiers.includes(tier)) throw new Error(model + " supports only " + spec.tiers.join(", "));
  request.resolution = tier;
  for (const group of MEDIA) {
    if ((request[group.names[1]] || []).length > spec[group.names[0]]) throw new Error(model + " supports at most " + spec[group.names[0]] + " " + group.names[0]);
  }
  if (model === "minimax-h3") {
    if (Array.from(request.prompt).length > 7000) throw new Error("minimax-h3 prompt exceeds 7000 characters");
    if (request.generate_audio === true) throw new Error("minimax-h3 does not support generate_audio=true");
    if (request.all_in_one_reference === true) throw new Error("minimax-h3 does not support all_in_one_reference=true");
  }
  if (request.web_search === true && !spec.search) throw new Error(model + " does not support web_search=true");
  if (request.video_extend === true && model !== "doubao-seedance-2-5") throw new Error(model + " does not support video_extend=true");
  return { model, request };
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string" || !credential.trim()) throw new Error("channel API key is required");
  const token = credential.trim().replace(/^Bearer\s+/i, "");
  if (!token || /\s/.test(token)) throw new Error("invalid channel API key");
  return { Authorization: "Bearer " + token };
}

export function buildSubmitRequest(ctx) {
  const normalized = modelRequest(ctx);
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos", method: "POST",
    headers: { ...authHeaders(ctx), "Content-Type": "application/json" },
    body: { model: normalized.model, ...normalized.request },
  };
}

export function extractUsage(ctx) {
  const normalized = modelRequest(ctx), request = normalized.request;
  return { seconds: request.duration, resolution: request.resolution, video_input: !!(request.video_urls || []).length,
    generate_audio: normalized.model !== "minimax-h3" && request.generate_audio !== false };
}

export function extractUsageOnComplete() {
  // Elapsed processing time and AKOOL credit costs are not output usage.
  return {};
}

export function describeSpec(ctx) {
  const request = modelRequest(ctx).request;
  return {
    spec_version: 3, output_seconds: request.duration, seconds_kind: "exact", resolution: request.resolution,
    references: { image: (request.image_urls || []).length, video: (request.video_urls || []).length, audio: (request.audio_urls || []).length, frame: 0 },
    reference_video_urls: request.video_urls || [],
  };
}

function videoURL(body) {
  if (!isObject(body) || body.status !== "succeeded" || body.error || !Array.isArray(body.data)) return "";
  const item = body.data[0];
  return isObject(item) && httpURL(item.url) ? item.url : "";
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  // Missing IDs are ambiguous, not proof of rejection. Do not retry paid work.
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim()) throw new Error("upstream response is missing a task id");
  const output = { taskId: body.id, taskData: body };
  const result = parseTaskResult({ taskId: body.id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId.trim()) throw new Error("upstream task id is required");
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim() || body.id !== ctx.taskId) return { status: "UNKNOWN", reason: "invalid or mismatched AK2API task id" };
  if (body.status === "failed" || body.status === "expired") {
    const error = isObject(body.error) ? body.error : {};
    const code = typeof error.code === "string" && error.code.trim() ? error.code.trim() : body.status === "expired" ? "UPSTREAM_TIMEOUT" : "UPSTREAM_FAILED";
    const message = typeof error.message === "string" && error.message.trim() ? error.message : "video generation failed";
    return { status: "FAILURE", reason: code + ": " + message };
  }
  if (body.error) return { status: "UNKNOWN", reason: "conflicting AK2API task response" };
  if (body.status === "succeeded" && videoURL(body)) return { status: "SUCCESS", progress: "100%" };
  const statuses = { queued: "QUEUED", preparing: "IN_PROGRESS", submitted: "SUBMITTED", running: "IN_PROGRESS" };
  if (!has(statuses, body.status)) return { status: "UNKNOWN", reason: "unrecognized task status or missing video URL" };
  const result = { status: statuses[body.status] };
  if (typeof body.progress === "number" && Number.isFinite(body.progress) && body.progress >= 0 && body.progress <= 100) result.progress = body.progress + "%";
  return result;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && videoURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const url = videoURL(ctx.data);
  if (ctx.artifactKey !== "video" || !url) throw new Error("artifact_not_found");
  // Follow the stored CDN URL directly, without forwarding channel credentials
  // through AK2API's authenticated /content redirect. Host SSRF/Range rules apply.
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/^(content_moderation_failed|media_download_failed):/.test(text)) return "user";
  // PROVIDER_INVALID_REQUEST may be an adapter/provider defect; do not exempt it.
  if (/^[a-z_]+:/.test(text)) return "upstream";
  if (/content moderation|content policy violation|内容.{0,12}(违规|敏感|不合规)/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      let input;
      if (body && body.kind === "json") input = body.value;
      else if (body && body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("AK2API requires media URLs; local file uploads are unsupported");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) {
            if (entries.length === 1 && entries[0].trim().startsWith("[")) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be a URL array"); }
            } else input[name] = Array.from(entries);
          } else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            if (BOOLEAN_FIELDS.includes(name) || name === "metadata" || name === "content") {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " contains invalid JSON"); }
            } else input[name] = name === "n" && entries[0] === "1" ? 1 : entries[0];
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Candidate decoding precedes mapping. Validate the final model when known.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody });
      return { kind: "submit", model: input.model, action: MEDIA.some(function (group) { return !!requestBody[group.names[1]]; }) ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      return url ? { url } : {};
    },
  },
};
