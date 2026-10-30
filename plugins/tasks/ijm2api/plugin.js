// IJM2API /v1/videos and app/catalog.py + app/service.py, checked 2026-10-16.
// Model aliases fix the output tier; sd-2-5-480p-x requires upstream opt-in
// and produces 480p by transcoding a 720p generation, with 720p upstream costs.
const MODEL_TIERS = {
  "sd-2-0": "720p", "sd-2-0-1080p": "1080p", "sd-2-0-4k": "4k",
  "sd-2-0-fast": "720p", "sd-2-0-mini": "720p",
  "sd-2-5": "720p", "sd-2-5-480p": "480p", "sd-2-5-480p-x": "480p",
  "sd-2-5-1080p": "1080p", "sd-2-5-480p-preview": "480p",
};
// Public family names select a native alias by resolution, defaulting to 720p.
// Keep explicit sd-* aliases fixed, including channel model_mapping targets.
const MODEL_VARIANTS = {
  "seedance-2.0": { "720p": "sd-2-0", "1080p": "sd-2-0-1080p", "4k": "sd-2-0-4k" },
  "seedance-2.0-fast": { "720p": "sd-2-0-fast" },
  "seedance-2.0-mini": { "720p": "sd-2-0-mini" },
  "seedance-2.5": { "480p": "sd-2-5-480p", "720p": "sd-2-5", "1080p": "sd-2-5-1080p" },
};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "9:21"];
const MEDIA = [
  { names: ["images", "image_urls"], limit: 30 },
  { names: ["videos", "video_urls"], limit: 10 },
  { names: ["audios", "audio_urls"], limit: 10 },
];
const MEDIA_FIELDS = MEDIA.flatMap(function (group) { return group.names; });
const FRAME_FIELDS = ["first_frame_url", "first_image_url", "last_frame_url", "last_image_url"];
const FIELDS = ["model", "prompt", "seconds", "duration", "resolution", "size", "ratio", "aspect_ratio", "n", "generate_audio"].concat(MEDIA_FIELDS, FRAME_FIELDS);

export const meta = {
  apiVersion: 1,
  key: "ijm2api",
  name: "IJM2API",
  version: "1.2.0",
  author: { name: "jiayi-1994" },
  icon: "text:IJ",
  description: { en: "Dreamina Seedance video generation through IJM2API", zh: "通过 IJM2API 生成 Dreamina Seedance 视频" },
  baseUrl: "http://82.158.230.113:8808",
  models: Object.keys(MODEL_TIERS).concat(Object.keys(MODEL_VARIANTS)),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  // The public API exposes no reliable credit quantity. Reserve requested
  // output seconds; administrator pricing and unified sales own the prices.
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
  },
  usageExamples: [{ label: "4s", facts: { seconds: 4 } }],
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
  // Stricter than relay/common.MaxTaskDurationSeconds; family caps follow mapping.
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+$/.test(String(value))) throw new Error("duration must be an integer between 4 and 30 seconds");
  const seconds = Number(value);
  if (!Number.isInteger(seconds) || seconds < 4 || seconds > 30) throw new Error("duration must be an integer between 4 and 30 seconds");
  return seconds;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be 480p, 720p, 1080p or 4k");
  const tier = value.toLowerCase() === "2160p" ? "4k" : value.toLowerCase();
  if (!["480p", "720p", "1080p", "4k"].includes(tier)) throw new Error("resolution must be 480p, 720p, 1080p or 4k");
  return tier;
}

function ratio(value) {
  if (!RATIOS.includes(value)) throw new Error("unsupported aspect ratio: " + String(value));
  return value;
}

function scalarAlias(input, names, parse) {
  let selected;
  for (const name of names) {
    if (!has(input, name)) continue;
    const value = parse(input[name]);
    if (selected !== undefined && selected !== value) throw new Error("conflicting values for " + names.join("/"));
    selected = value;
  }
  return selected;
}

// Shared by decoding, scheduling, billing and submission, including overrides.
// Equal aliases represent one list; repeats within that list remain separate inputs.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const name of Object.keys(input)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  if (has(input, "n") && input.n !== 1) throw new Error("n must be 1");
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  const seconds = scalarAlias(input, ["seconds", "duration"], duration);
  let tier = has(input, "resolution") ? resolution(input.resolution) : undefined;
  let aspect = scalarAlias(input, ["ratio", "aspect_ratio"], ratio);
  if (has(input, "size")) {
    if (typeof input.size !== "string") throw new Error("size must be a resolution tier or WIDTHxHEIGHT");
    const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(input.size);
    let sizeTier;
    if (match) {
      const width = Number(match[1]);
      const height = Number(match[2]);
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(function (value) {
        const parts = value.split(":");
        return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01;
      });
      if (!sizeRatio) throw new Error("size has an unsupported aspect ratio");
      if (aspect !== undefined && aspect !== sizeRatio) throw new Error("size conflicts with ratio");
      aspect = sizeRatio;
    } else sizeTier = resolution(input.size);
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    tier = sizeTier;
  }
  const result = { prompt: input.prompt.trim(), duration: seconds === undefined ? 4 : seconds, aspect_ratio: aspect || "16:9" };
  // Do not freeze a model's default tier before channel model_mapping runs.
  if (tier !== undefined) result.resolution = tier;
  if (has(input, "generate_audio")) {
    if (typeof input.generate_audio !== "boolean") throw new Error("generate_audio must be a boolean");
    // Canvas compatibility only: IJM2API currently drops this in normalize_request.
    result.generate_audio = input.generate_audio;
  }
  for (const group of MEDIA) {
    let selected;
    for (const name of group.names) {
      if (!has(input, name)) continue;
      const urls = input[name];
      if (!Array.isArray(urls) || urls.length > group.limit || urls.some(function (url) { return !httpURL(url); })) throw new Error(name + " must be an array of at most " + group.limit + " HTTP(S) URLs without credentials");
      if (selected !== undefined && JSON.stringify(selected) !== JSON.stringify(urls)) throw new Error("conflicting reference fields: " + group.names.join("/"));
      selected = Array.from(urls);
    }
    if (selected && selected.length) result[group.names[1]] = selected;
  }
  for (const names of [["first_frame_url", "first_image_url"], ["last_frame_url", "last_image_url"]]) {
    const value = scalarAlias(input, names, function (url) {
      if (!httpURL(url)) throw new Error(names[0] + " must be an HTTP(S) image URL without credentials");
      return url;
    });
    if (value !== undefined) result[names[0]] = value;
  }
  if (result.last_frame_url && !result.first_frame_url) throw new Error("last_frame_url requires first_frame_url");
  if (result.first_frame_url && MEDIA.some(function (group) { return !!result[group.names[1]]; })) throw new Error("first/last frames cannot be combined with reference images, videos or audio");
  return result;
}

function modelRequest(ctx) {
  let model = ctx.upstreamModel || ctx.model;
  if (!has(MODEL_TIERS, model) && !has(MODEL_VARIANTS, model)) throw new Error("unsupported IJM2API model: " + String(model));
  const request = videoRequest(ctx.requestBody);
  if (has(MODEL_VARIANTS, model)) {
    const variants = MODEL_VARIANTS[model];
    const tier = request.resolution === undefined ? "720p" : request.resolution;
    if (!has(variants, tier)) throw new Error(model + " supports only " + Object.keys(variants).join(", "));
    model = variants[tier];
  }
  const is25 = model.startsWith("sd-2-5");
  if (!is25 && request.duration > 15) throw new Error(model + " supports duration from 4 to 15 seconds");
  if (request.resolution !== undefined && request.resolution !== MODEL_TIERS[model]) throw new Error(model + " uses " + MODEL_TIERS[model] + "; choose the corresponding model alias");
  request.resolution = MODEL_TIERS[model];
  for (const group of MEDIA) {
    const name = group.names[1];
    const limit = is25 ? group.limit : name === "image_urls" ? 9 : 3;
    if ((request[name] || []).length > limit) throw new Error(model + " supports at most " + limit + " " + group.names[0]);
  }
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
  return { seconds: modelRequest(ctx).request.duration };
}

export function extractUsageOnComplete() {
  // The public task response has no measured duration/credit usage. Preserve
  // reserved seconds, never infer usage from elapsed time or account balance.
  return {};
}

export function describeSpec(ctx) {
  const request = modelRequest(ctx).request;
  return {
    spec_version: 3, output_seconds: request.duration, seconds_kind: "exact", resolution: request.resolution,
    references: { image: (request.image_urls || []).length, video: (request.video_urls || []).length, audio: (request.audio_urls || []).length, frame: Number(!!request.first_frame_url) + Number(!!request.last_frame_url) },
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
  // No documented 2xx rejection envelope exists. Missing IDs are ambiguous,
  // so throwing prevents automatic resubmission of possibly accepted work.
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
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim() || body.id !== ctx.taskId) return { status: "UNKNOWN", reason: "invalid or mismatched IJM2API task id" };
  if (body.status === "failed" || body.status === "expired") {
    const error = isObject(body.error) ? body.error : {};
    const code = typeof error.code === "string" && error.code.trim() ? error.code.trim() : body.status === "expired" ? "UPSTREAM_TIMEOUT" : "UPSTREAM_FAILED";
    const message = typeof error.message === "string" && error.message.trim() ? error.message : "video generation failed";
    return { status: "FAILURE", reason: code + ": " + message };
  }
  if (body.error) return { status: "UNKNOWN", reason: "conflicting IJM2API task response" };
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
  // CDN downloads carry no channel/client credentials. The host applies SSRF
  // validation and Range handling. The stored URL retains the vendor's expiry.
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  // Stable codes take precedence over localized/public error text.
  if (/^(content_moderation_failed|video_edit_constraint|media_download_failed|media_limit_exceeded|media_format_unsupported):/.test(text)) return "user";
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
        if ((body.files || []).length) throw new Error("IJM2API requires media URLs; local file uploads are unsupported");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) {
            if (entries.length === 1 && entries[0].startsWith("[")) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be a URL array"); }
            } else input[name] = entries;
          } else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            if (name === "generate_audio") {
              if (entries[0] !== "true" && entries[0] !== "false") throw new Error("generate_audio must be a boolean");
              input[name] = entries[0] === "true";
            } else input[name] = name === "n" && entries[0] === "1" ? 1 : entries[0];
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Initial candidate decoding precedes mapping; apply model-specific
      // defaults and caps only after the final upstream model is known.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody });
      return { kind: "submit", model: input.model, action: requestBody.first_frame_url || MEDIA.some(function (group) { return !!requestBody[group.names[1]]; }) ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      // Do not expose IJM2API's account_id or private task identifiers.
      return url ? { url } : {};
    },
  },
};
