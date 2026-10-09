// Task Plugin API v1 for all 21 Seedance models returned by /api/user/models.
// Each entry was checked against /api/pricing and its own /docs/models/{model}
// page on 2026-10-08. Never infer a model's capabilities from its name or price.
// Prices stay in host configuration; the provider's displayed currency is CNY.
const RATIOS = ["16:9", "9:16", "1:1", "21:9", "4:3", "3:4"];
const RESOLUTIONS = ["480p", "720p", "1080p", "4k"];
const HOST_MAX_SECONDS = 3600; // relay/common.MaxTaskDurationSeconds
const HOST_MAX_REFERENCES = 1024; // pkg/videosched/spec.MaxReferenceCount
// SD10's missing video/audio/asset/face fields were supplied by the integrator.
// null means an unpublished media-count limit, not the three-item Ark limit.
// Explicit per-model prohibitions (SD13 audio, SD15-2.5 video, SD8, etc.) remain.
// frames: pair = both required; exclusive = first-only allowed, no references;
// mixed = first-only allowed alongside reference images; none = unsupported.
const MODELS = {
  "sd10-seedance-2.0":      { seconds: [5, 10, 15], resolutions: ["720p"], defaultResolution: "720p", ratios: RATIOS, defaultRatio: "9:16", images: 9, videos: null, audios: null, frames: "none", camera: true, face: true, assets: true, promptMax: 6000 },
  "sd10-seedance-2.0-fast": { seconds: [5, 10, 15], resolutions: ["720p"], defaultResolution: "720p", ratios: RATIOS, defaultRatio: "9:16", images: 9, videos: null, audios: null, frames: "none", camera: true, face: true, assets: true, promptMax: 6000 },
  "sd10-seedance-2.0-mini": { seconds: [5, 10],     resolutions: ["720p"], defaultResolution: "720p", ratios: RATIOS, defaultRatio: "9:16", images: 9, videos: null, audios: null, frames: "none", camera: true, face: true, assets: true, promptMax: 6000 },
  "sd10-seedance-2.5":      { seconds: [30], defaultSeconds: 30, resolutions: ["720p"], defaultResolution: "720p", ratios: RATIOS, defaultRatio: "9:16", images: 30, videos: null, audios: null, frames: "none", camera: true, face: true, assets: true, promptMax: 6000 },
  "sd11-seedance-2.0":      { minSeconds: 4, maxSeconds: 15, defaultSeconds: 7, resolutions: ["480p", "720p", "1080p"], defaultResolution: "480p", ratios: RATIOS.concat("adaptive"), images: 9, videos: 3, audios: 3, frames: "mixed", audio: true, seed: "nonnegative" },
  "sd11-seedance-2.0-fast": { minSeconds: 4, maxSeconds: 15, defaultSeconds: 7, resolutions: ["480p", "720p"], defaultResolution: "480p", ratios: RATIOS.concat("adaptive"), images: 9, videos: 3, audios: 3, frames: "mixed", audio: true, seed: "nonnegative" },
  "sd11-seedance-2.0-mini": { minSeconds: 4, maxSeconds: 15, defaultSeconds: 7, resolutions: ["480p", "720p"], defaultResolution: "480p", ratios: RATIOS.concat("adaptive"), images: 9, videos: 3, audios: 3, frames: "mixed", audio: true, seed: "nonnegative" },
  "sd11-seedance-2.5":      { minSeconds: 4, maxSeconds: 30, defaultSeconds: 5, resolutions: ["480p", "720p", "1080p"], defaultResolution: "480p", ratios: RATIOS.concat("adaptive"), images: 30, videos: 10, audios: 10, frames: "mixed", audio: true, seed: "nonnegative" },
  "sd13-seedance-2.0":      { minSeconds: 4, maxSeconds: 15, defaultSeconds: 8, resolutions: ["480p", "720p", "1080p", "4k"], defaultResolution: "480p", ratios: RATIOS.concat("auto"), defaultRatio: "auto", images: 9, videos: 3, audios: 0, frames: "none", audio: true },
  "sd13-seedance-2.0-fast": { minSeconds: 4, maxSeconds: 15, defaultSeconds: 8, resolutions: ["480p", "720p"], defaultResolution: "480p", ratios: RATIOS.concat("auto"), defaultRatio: "auto", images: 9, videos: 3, audios: 0, frames: "none", audio: true },
  "sd13-seedance-2.0-mini": { minSeconds: 4, maxSeconds: 15, defaultSeconds: 8, resolutions: ["480p", "720p"], defaultResolution: "480p", ratios: RATIOS.concat("auto"), defaultRatio: "auto", images: 9, videos: 3, audios: 0, frames: "none", audio: true },
  "sd13-seedance-2.5":      { minSeconds: 4, maxSeconds: 30, defaultSeconds: 5, resolutions: ["480p", "720p"], defaultResolution: "480p", ratios: RATIOS.concat("auto"), defaultRatio: "auto", images: 30, videos: 10, audios: 0, frames: "none", audio: true },
  "sd14-seedance-2.0":      { minSeconds: 4, maxSeconds: 15, resolutions: ["720p"], defaultResolution: "720p", ratios: ["1:1", "16:9", "9:16"], images: 9, videos: 3, audios: 3, frames: "exclusive" },
  "sd15-seedance-2.0":      { minSeconds: 4, maxSeconds: 15, defaultSeconds: 5, resolutions: ["480p", "720p"], defaultResolution: "720p", ratios: ["16:9", "9:16", "1:1"], images: 9, videos: 3, audios: 3, frames: "none" },
  "sd15-seedance-2.5":      { minSeconds: 4, maxSeconds: 30, defaultSeconds: 5, resolutions: ["480p", "720p"], defaultResolution: "720p", ratios: ["16:9", "9:16", "1:1"], images: 30, videos: 0, audios: 10, frames: "none" },
  "sd7-seedance-2.0-1080p": { minSeconds: 4, maxSeconds: 15, resolutions: ["1080p"], defaultResolution: "1080p", ratios: RATIOS, images: 5, videos: 3, audios: 3, frames: "none", audio: true },
  "sd7-seedance-2.0-720p":  { minSeconds: 4, maxSeconds: 15, resolutions: ["720p"], defaultResolution: "720p", ratios: RATIOS, images: 5, videos: 3, audios: 3, frames: "none", audio: true },
  // No published resolution tier: never invent 720p for quoting or send it.
  "sd8-seedance-2.5":       { seconds: [30], defaultSeconds: 30, resolutions: [], ratios: RATIOS, images: 9, videos: 0, audios: 0, frames: "none" },
  "doubao-seedance-2-0-260128":      { billing: "token", minSeconds: 4, maxSeconds: 15, defaultSeconds: 4, resolutions: ["720p"], defaultResolution: "720p", ratios: RATIOS, defaultRatio: "16:9", images: 9, videos: 3, audios: 3, frames: "pair", audio: true, seed: "integer", face: true, assets: true },
  "doubao-seedance-2-0-fast-260128": { billing: "token", minSeconds: 4, maxSeconds: 15, defaultSeconds: 4, resolutions: ["720p"], defaultResolution: "720p", ratios: RATIOS, defaultRatio: "16:9", images: 9, videos: 3, audios: 3, frames: "pair", audio: true, seed: "integer", face: true, assets: true },
  "doubao-seedance-2-5-260628":      { billing: "token", minSeconds: 4, maxSeconds: 30, defaultSeconds: 4, resolutions: ["720p"], defaultResolution: "720p", ratios: RATIOS, defaultRatio: "16:9", images: 9, videos: 3, audios: 3, frames: "pair", audio: true, seed: "integer", face: true, assets: true },
};
const REQUEST_PRICE = { type: "number", unit: "count", description: { en: "Video generation unit price", zh: "视频生成单价" } };
const MEDIA = [
  { field: "reference_image_urls", limit: "images", aliases: ["reference_image_urls", "reference_images", "referenceImages", "images", "image", "image_url", "input_reference"] },
  { field: "reference_videos", limit: "videos", aliases: ["reference_videos", "referenceVideos", "videos"] },
  { field: "reference_audios", limit: "audios", aliases: ["reference_audios", "referenceAudios", "audios", "audio_urls"] },
];
const MEDIA_FIELDS = MEDIA.flatMap(kind => kind.aliases);
const FIELDS = [
  "model", "prompt", "duration", "seconds", "resolution", "size", "n",
  "aspect_ratio", "ratio", "camera_movement", "generate_audio", "face_mode",
  "seed", "first_image_url", "last_image_url", "first_image", "last_image",
].concat(MEDIA_FIELDS);

export const meta = {
  apiVersion: 1,
  key: "cangyuan",
  name: "沧元算力 Seedance",
  version: "1.1.1",
  author: { name: "jiayi-1994" },
  description: {
    en: "Seedance video generation through Cangyuan, with official relay models requiring unified video sales",
    zh: "通过沧元算力生成 Seedance 视频，官转模型需启用统一视频销售",
  },
  icon: "text:沧元",
  website: "https://ai.cangyuansuanli.cn",
  baseUrl: "https://ai.cangyuansuanli.cn",
  // Keep the official Ark plugin and its token pricing out of this relay pool.
  models: Object.keys(MODELS).map(model => MODELS[model].billing === "token" ? "cangyuan-" + model : model),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: { requests: REQUEST_PRICE },
  // The official relay is token-priced, but its public API does not document
  // token estimation/settlement fields. It is usable through the host-owned
  // unified sale only, never an invented seconds-to-token conversion.
  usageProfiles: Object.keys(MODELS).map(function (model) {
    const profile = MODELS[model];
    if (profile.billing === "token") return { models: ["cangyuan-" + model], schema: {} };
    const schema = { requests: REQUEST_PRICE };
    if (profile.resolutions.length) schema.resolution = {
      enum: profile.resolutions,
      description: { en: "Output video resolution", zh: "输出视频分辨率" },
    };
    return { models: [model], schema };
  }),
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function upstreamModel(name) {
  const raw = typeof name === "string" && name.startsWith("cangyuan-") ? name.slice("cangyuan-".length) : name;
  if (has(MODELS, raw) && (raw === name || MODELS[raw].billing === "token")) return raw;
  throw new Error("unsupported Cangyuan model: " + name);
}

function seconds(value) {
  if ((typeof value !== "string" && typeof value !== "number") || !/^[1-9]\d*$/.test(String(value))) {
    throw new Error("duration must be an integer from 1 to " + HOST_MAX_SECONDS + " seconds");
  }
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed > HOST_MAX_SECONDS) throw new Error("duration must be an integer from 1 to " + HOST_MAX_SECONDS + " seconds");
  return parsed;
}

function httpURL(value) {
  return typeof value === "string" && value.length <= 8192 &&
    /^https?:\/\/[^\s/?#@\\%]+(?:[/?#][^\s\\]*)?$/.test(value) && !/[\u0000-\u001f\u007f]/.test(value);
}

function httpsURL(value) {
  return httpURL(value) && value.startsWith("https://");
}

function mediaURL(value, field) {
  if (typeof value !== "string" || (!httpsURL(value) && !/^asset:\/\/[A-Za-z0-9_-]+$/.test(value))) {
    throw new Error(field + " must contain HTTPS URLs without credentials or asset:// identifiers");
  }
  return value;
}

function mediaReferences(input, kind) {
  let result;
  for (const name of kind.aliases) {
    if (!has(input, name)) continue;
    let value = input[name];
    if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && value[0].trim().startsWith("[")) value = value[0];
    if (typeof value === "string") {
      if (value.trim().startsWith("[")) {
        try { value = JSON.parse(value); } catch (_error) { throw new Error(name + " must be a URL array"); }
      } else value = [value];
    }
    if (!Array.isArray(value) || value.length > HOST_MAX_REFERENCES) throw new Error(name + " must be a URL array with at most " + HOST_MAX_REFERENCES + " entries");
    const urls = value.map(url => mediaURL(url, name));
    if (result !== undefined && (result.length !== urls.length || result.some((url, i) => url !== urls[i]))) {
      throw new Error("conflicting reference fields for " + kind.field);
    }
    // Identical aliases are mirrors; repeated entries within one array remain
    // separate submitted references and must be counted independently.
    result = urls;
  }
  return result || [];
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be a supported resolution string");
  const tier = value.toLowerCase() === "2160p" ? "4k" : value.toLowerCase();
  if (!RESOLUTIONS.includes(tier)) throw new Error("unsupported resolution: " + value);
  return tier;
}

// One validation chain for candidate decoding, cost quotes and final submission,
// including the body after administrator parameter overrides.
function videoParams(input, model) {
  if (!isObject(input)) throw new Error("video request must be an object");
  input = Object.assign({}, input);
  for (const name of ["first_image", "last_image"]) {
    if (!has(input, name)) continue;
    const url = mediaURL(input[name], name);
    if (has(input, name + "_url") && mediaURL(input[name + "_url"], name + "_url") !== url) throw new Error(name + " conflicts with " + name + "_url");
    input[name + "_url"] = url;
    delete input[name];
  }
  const profile = model ? MODELS[model] : undefined;
  for (const key of Object.keys(input)) {
    if (!FIELDS.includes(key)) throw new Error("unsupported video parameter: " + key);
  }
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  if (profile && profile.promptMax && Array.from(input.prompt).length > profile.promptMax) throw new Error(model + " prompt must not exceed " + profile.promptMax + " characters");
  if (has(input, "n") && input.n !== 1 && input.n !== "1") throw new Error("n must be 1");

  const output = { prompt: input.prompt };
  for (const name of ["duration", "seconds"]) {
    if (!has(input, name)) continue;
    const value = seconds(input[name]);
    if (output.duration !== undefined && output.duration !== value) throw new Error("seconds conflicts with duration");
    output.duration = value;
  }
  if (profile) {
    if (output.duration === undefined) output.duration = profile.defaultSeconds;
    if (output.duration === undefined) throw new Error(model + " requires duration or seconds");
    if (profile.seconds && !profile.seconds.includes(output.duration)) throw new Error(model + " duration must be one of " + profile.seconds.join(", ") + " seconds");
    if (!profile.seconds && (output.duration < profile.minSeconds || output.duration > profile.maxSeconds)) {
      throw new Error(model + " duration must be from " + profile.minSeconds + " to " + profile.maxSeconds + " seconds");
    }
  }

  if (has(input, "resolution")) output.resolution = resolution(input.resolution);
  for (const name of ["aspect_ratio", "ratio"]) {
    if (!has(input, name)) continue;
    const ratios = profile ? profile.ratios : RATIOS.concat("auto", "adaptive");
    if (!ratios.includes(input[name])) throw new Error(name + " must be one of " + ratios.join(", "));
    if (output.aspect_ratio && output.aspect_ratio !== input[name]) throw new Error("ratio conflicts with aspect_ratio");
    output.aspect_ratio = input[name];
  }
  if (has(input, "size")) {
    const match = typeof input.size === "string" && /^(\d{3,4})x(\d{3,4})$/.exec(input.size);
    let tier;
    if (match) {
      tier = { 480: "480p", 720: "720p", 1080: "1080p", 2160: "4k" }[Math.min(Number(match[1]), Number(match[2]))];
      if (!tier) throw new Error("size must describe a supported resolution");
      const aspect = Number(match[1]) / Number(match[2]);
      let ratio;
      for (const candidate of RATIOS) {
        const parts = candidate.split(":");
        if (Math.abs(aspect - Number(parts[0]) / Number(parts[1])) < 0.01) ratio = candidate;
      }
      if (!ratio) throw new Error("size does not match a supported aspect ratio");
      if (output.aspect_ratio && output.aspect_ratio !== ratio) throw new Error("size conflicts with aspect_ratio");
      if (profile && !profile.ratios.includes(ratio)) throw new Error(model + " does not support size aspect ratio " + ratio);
      output.aspect_ratio = ratio;
    } else tier = resolution(input.size);
    if (output.resolution && output.resolution !== tier) throw new Error("size conflicts with resolution");
    output.resolution = tier;
  }
  if (profile) {
    if (!profile.resolutions.length && (has(input, "resolution") || has(input, "size"))) throw new Error(model + " does not accept resolution or size");
    if (output.resolution && !profile.resolutions.includes(output.resolution)) throw new Error(model + " resolution must be one of " + profile.resolutions.join(", "));
    if (!output.resolution && profile.defaultResolution) output.resolution = profile.defaultResolution;
    if (!output.aspect_ratio && profile.defaultRatio) output.aspect_ratio = profile.defaultRatio;
  }

  for (const kind of MEDIA) {
    const urls = mediaReferences(input, kind);
    if (profile && profile[kind.limit] !== null && urls.length > profile[kind.limit]) {
      if (profile[kind.limit] === 0) throw new Error(model + " does not support " + kind.field);
      throw new Error(model + " supports at most " + profile[kind.limit] + " " + kind.field + " entries");
    }
    if (urls.length) output[kind.field] = urls;
  }
  const frames = has(input, "first_image_url") || has(input, "last_image_url");
  if (frames) {
    if (profile && profile.frames === "none") throw new Error(model + " does not support first_image_url or last_image_url");
    if (!has(input, "first_image_url")) throw new Error("last_image_url requires first_image_url");
    if (profile && profile.frames === "pair" && !has(input, "last_image_url")) throw new Error("first_image_url and last_image_url must be supplied together");
    if (profile && profile.frames !== "mixed" && MEDIA.some(kind => (output[kind.field] || []).length)) throw new Error("first/last frames cannot be combined with reference arrays");
    if (profile && profile.frames === "mixed" && (output.reference_videos || output.reference_audios)) throw new Error(model + " first/last frames can only be combined with reference images");
    output.first_image_url = mediaURL(input.first_image_url, "first_image_url");
    if (has(input, "last_image_url")) output.last_image_url = mediaURL(input.last_image_url, "last_image_url");
  }
  const urls = MEDIA.flatMap(kind => output[kind.field] || []).concat([output.first_image_url, output.last_image_url].filter(Boolean));
  if (profile && !profile.assets && urls.some(url => url.startsWith("asset://"))) throw new Error(model + " requires HTTPS references; asset:// is not supported");
  for (const name of ["generate_audio", "face_mode"]) {
    if (!has(input, name)) continue;
    if (typeof input[name] !== "boolean") throw new Error(name + " must be a boolean");
    if (profile && !(name === "generate_audio" ? profile.audio : profile.face)) throw new Error(model + " does not support " + name);
    output[name] = input[name];
  }
  if (has(input, "face_mode")) {
    if (urls.some(url => url.startsWith("asset://"))) throw new Error("omit face_mode when using asset:// references");
  }
  if (has(input, "camera_movement")) {
    if (profile && !profile.camera) throw new Error(model + " does not support camera_movement");
    if (!["auto", "fixed"].includes(input.camera_movement)) throw new Error("camera_movement must be auto or fixed");
    output.camera_movement = input.camera_movement;
  }
  if (has(input, "seed")) {
    if (profile && !profile.seed) throw new Error(model + " does not support seed");
    if (!Number.isSafeInteger(input.seed)) throw new Error("seed must be a safe integer");
    if (profile && profile.seed === "nonnegative" && input.seed < 0) throw new Error(model + " seed must be non-negative");
    output.seed = input.seed;
  }
  return output;
}

function modelRequest(ctx) {
  const model = upstreamModel(ctx.upstreamModel || ctx.model);
  if (isObject(ctx.requestBody) && has(ctx.requestBody, "model") && ctx.requestBody.model !== ctx.model && ctx.requestBody.model !== ctx.upstreamModel) {
    throw new Error("request body model conflicts with the selected model");
  }
  return Object.assign({ model }, videoParams(ctx.requestBody, model));
}

function baseURL(ctx) {
  if (typeof ctx.baseUrl !== "string" || !/^https?:\/\/[^\s/?#@\\%]+(?:\/[^\s?#\\]*)?$/.test(ctx.baseUrl)) {
    throw new Error("channel Base URL must be an http(s) URL without credentials, query or fragment");
  }
  const base = ctx.baseUrl.replace(/\/+$/, "");
  if (/\/v1$/i.test(base)) throw new Error("channel Base URL must not end with /v1");
  return base;
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string") throw new Error("channel API key is required");
  const token = credential.replace(/^Bearer\s+/i, "").trim();
  if (!token || /\s/.test(token)) throw new Error("channel API key must not be empty or contain whitespace");
  return { Authorization: "Bearer " + token };
}

export function buildSubmitRequest(ctx) {
  const body = modelRequest(ctx);
  if (MODELS[body.model].billing === "token" && ctx.salesSource !== "video_request") {
    throw new Error("Cangyuan official Seedance requires host-configured unified video sales; token usage is not documented");
  }
  return {
    url: baseURL(ctx) + "/v1/videos", method: "POST",
    headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json" }), body,
  };
}

export function extractUsage(ctx) {
  const body = modelRequest(ctx);
  if (MODELS[body.model].billing !== "token") {
    const facts = { requests: 1 };
    if (body.resolution) facts.resolution = body.resolution;
    return facts;
  }
  if (ctx.salesSource !== "video_request") throw new Error("Cangyuan official Seedance requires host-configured unified video sales; token usage is not documented");
  return {};
}

export function extractUsageOnComplete() {
  // SD variants are per-generation and resolution. Official routes use a frozen host-owned sale.
  // Provider money, echoed duration and undocumented tokens cannot change either.
  return {};
}

export function describeSpec(ctx) {
  const body = modelRequest(ctx);
  const profile = MODELS[body.model];
  const videos = body.reference_videos || [];
  const spec = {
    spec_version: 3, output_seconds: body.duration,
    seconds_kind: profile.seconds && profile.seconds.length === 1 ? "fixed" : "exact",
    resolution: body.resolution || "*",
    references: {
      image: (body.reference_image_urls || []).length,
      video: videos.length, audio: (body.reference_audios || []).length,
      frame: (body.first_image_url ? 1 : 0) + (body.last_image_url ? 1 : 0),
    },
  };
  // Never publish a partial list: asset IDs cannot be read by the host's MP4
  // duration probe. Per-input-second quotes must exclude such a candidate.
  if (videos.every(httpsURL)) spec.reference_video_urls = videos;
  return spec;
}

function resultURL(body) {
  if (!isObject(body)) return undefined;
  if (httpURL(body.video_url)) return body.video_url;
  if (httpURL(body.url)) return body.url; // SD13 model documentation also names url.
  if (Array.isArray(body.data) && isObject(body.data[0]) && httpURL(body.data[0].url)) return body.data[0].url;
  return undefined;
}

function failureReason(body) {
  if (isObject(body.error)) {
    if (body.error.code === "video_request_rejected") return "video request rejected";
    if (body.error.code === "video_generation_cancelled") return "cancelled: video generation cancelled";
    if (typeof body.error.message === "string" && body.error.message.trim()) return body.error.message;
  }
  for (const value of [body.error, body.message, body.fail_reason]) {
    if (typeof value === "string" && value.trim()) return value;
  }
  return "Cangyuan video generation failed";
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "invalid task response" };
  for (const id of [body.id, body.task_id]) {
    if (id != null && (typeof id !== "string" || !id.trim() || (ctx.taskId && id !== ctx.taskId))) {
      return { status: "UNKNOWN", reason: "upstream task id does not match the queried task" };
    }
  }
  const statuses = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE" };
  if (typeof body.status !== "string" || !has(statuses, body.status)) return { status: "UNKNOWN", reason: "unrecognized Cangyuan task status" };
  const status = statuses[body.status];
  if (status !== "FAILURE" && (body.error || body.success === false)) return { status: "UNKNOWN", reason: "conflicting task status and error" };
  if (status === "SUCCESS" && !resultURL(body)) return { status: "UNKNOWN", reason: "completed task has no valid video URL" };
  const result = { status };
  if (status === "FAILURE") result.reason = failureReason(body);
  if (typeof body.progress === "number" && Number.isFinite(body.progress) && body.progress >= 0 && body.progress <= 100) result.progress = body.progress + "%";
  return result;
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("upstream create response must be an object");
  const ids = [body.id, body.task_id].filter(id => id != null);
  if (ids.some(id => typeof id !== "string" || !id.trim()) || (ids.length === 2 && ids[0] !== ids[1])) {
    throw new Error("upstream create response has invalid or conflicting task IDs");
  }
  const taskId = ids[0];
  if (!taskId) {
    // Explicit refusal is safe to attribute/retry; an unknown outcome is not.
    if ((body.error || body.success === false || body.status === "failed") && (!body.status || body.status === "failed")) {
      return { rejected: { reason: failureReason(body) } };
    }
    throw new Error("upstream create response has no task id");
  }
  if ((body.error || body.success === false) && body.status !== "failed") throw new Error("upstream create response has conflicting acceptance and rejection signals");
  const output = { taskId, taskData: body };
  const result = parseTaskResult({ taskId }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId.trim()) throw new Error("upstream task id is required");
  return { url: baseURL(ctx) + "/v1/videos/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && resultURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const url = resultURL(ctx.data);
  if (!url) throw new Error("completed task has no valid video URL");
  // The provider documents an opaque public download URL. Preserve its signature
  // and never forward channel/client credentials or rewrite it to /content.
  return { url, method: ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET", credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (text === "video request rejected") return "user";
  if (/^cancelled: |cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/timeout|timed out|service (is temporarily )?unavailable|internal (server )?error|quota (exhausted|exceeded)|insufficient (quota|balance)|rate limit|connection|authentication|超时|服务(暂)?不可用|内部错误|额度(不足|耗尽)|限流/.test(text)) return "upstream";
  if (/content policy violation|content violation|moderation (rejected|blocked)|rejected by (the )?(content )?moderation|审核(未通过|不通过|拒绝)|未通过(内容)?审核|内容.{0,12}(违规|敏感|不合规)|invalid (image|video|audio|prompt|url)\b|unsupported (image|video|audio) (format|type)\b/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      if (!body || !["json", "multipart"].includes(body.kind)) throw new Error("JSON or multipart text fields are required");
      let input = body.value;
      if (body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("local file uploads are not supported; supply HTTPS URLs or existing asset:// identifiers");
        input = {};
        for (const raw of Object.keys(body.fields || {})) {
          const name = raw.endsWith("[]") ? raw.slice(0, -2) : raw;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate field: " + name);
          const entries = body.fields[raw];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) { input[name] = entries; continue; }
          if (entries.length !== 1) throw new Error(name + " must be provided once");
          let value = entries[0];
          if (["face_mode", "generate_audio"].includes(name)) {
            if (value !== "true" && value !== "false") throw new Error(name + " must be true or false");
            value = value === "true";
          }
          if (name === "seed") {
            if (!/^-?\d+$/.test(value)) throw new Error("seed must be an integer");
            value = Number(value);
          }
          input[name] = value;
        }
      }
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && ctx.model !== input.model) throw new Error("model does not match the selected model");
      // Channel mapping is unavailable during the first candidate decode. Leave
      // model-dependent defaults pending for arbitrary public aliases.
      let model;
      if (ctx.upstreamModel) model = upstreamModel(ctx.upstreamModel);
      else if (has(MODELS, input.model) || meta.models.includes(input.model)) model = upstreamModel(input.model);
      const requestBody = videoParams(input, model);
      const referenced = MEDIA.some(kind => (requestBody[kind.field] || []).length) || requestBody.first_image_url;
      return { kind: "submit", model: input.model, action: referenced ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const output = {};
      if (task.status === "SUCCESS" && resultURL(task.data)) output.video_url = resultURL(task.data);
      if (task.status === "FAILURE") output.error = { message: task.fail_reason || failureReason(isObject(task.data) ? task.data : {}) };
      return output;
    },
  },
};
