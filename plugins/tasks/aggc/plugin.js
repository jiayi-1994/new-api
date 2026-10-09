// Task Plugin API v1 for https://aggc.site/docs.
// Public video capabilities: https://aggc.site/api/v1/models (2026-10-07).
// Exclude the whitelist-only seedance-2.0 test model and image models.
// Empty tiers mean no selectable resolution, not an implicit 720p output.
const MODELS = {
  "sd2.0-933-op":      { min: 4, max: 15, tiers: ["480p", "720p", "1080p", "4k"], images: 9, videos: 3, audios: 3 },
  "sd2.0-933-mm":      { min: 4, max: 15, tiers: [], images: 9, videos: 0, audios: 3, face: true },
  "sd2.0-933-oc":      { min: 4, max: 15, tiers: [], images: 9, videos: 3, audios: 3 },
  "sd2.0-933-oa":      { min: 4, max: 15, tiers: [], images: 9, videos: 3, audios: 3 },
  "sd2.0-933-qd":      { min: 4, max: 15, tiers: ["480p", "720p", "1080p"], images: 9, videos: 3, audios: 3 },
  "sd2.0-fast-933-op": { min: 4, max: 15, tiers: ["480p", "720p"], images: 9, videos: 3, audios: 3 },
  "sd2.0-mini-933-op": { min: 4, max: 15, tiers: ["480p", "720p"], images: 9, videos: 3, audios: 3 },
  "sd2.0-fast-933-qd": { min: 4, max: 15, tiers: ["480p", "720p"], images: 9, videos: 3, audios: 3 },
  "sd2.0-mini-933-qd": { min: 4, max: 15, tiers: ["480p", "720p"], images: 9, videos: 3, audios: 3 },
  "sd2.5-op":         { min: 4, max: 30, tiers: ["480p", "720p", "1080p"], images: 30, videos: 10, audios: 10 },
  "sd2.5-qd":         { min: 4, max: 30, tiers: ["480p", "720p", "1080p"], images: 30, videos: 10, audios: 10 },
  "sd2.5-mm":         { min: 4, max: 30, tiers: [], images: 30, videos: 0, audios: 10, face: true },
  "sd2.5-m2":         { min: 4, max: 30, tiers: [], images: 10, videos: 0, audios: 10, face: true },
  // The parameter enum starts at 5s, despite the catalog description saying 4s.
  "wan3.0":           { min: 5, max: 30, tiers: ["720p", "1080p"], images: 9, videos: 3, audios: 3 },
  // Keep the official MiniMax-H3 plugin's routing/price metadata independent.
  // This public name is translated to the vendor's minimax-h3 on submission.
  "aggc-minimax-h3":  { min: 4, max: 15, tiers: ["768p"], images: 9, videos: 3, audios: 3 },
};
const RESOLUTIONS = ["480p", "720p", "768p", "1080p", "4k"];
const RATIOS = ["16:9", "9:16", "1:1", "3:4", "4:3", "21:9"];
const MEDIA_GROUPS = [
  ["imageUrls", "images", "referenceImages", "reference_images", "image", "input_reference"],
  ["videoUrls", "videos", "referenceVideos", "reference_videos"],
  ["audioUrls", "audios", "referenceAudios", "reference_audios"],
];
const MEDIA_FIELDS = MEDIA_GROUPS.flat();
const PARAM_FIELDS = ["duration", "aspectRatio", "resolution", "imageUrls", "videoUrls", "audioUrls", "faceModeEnabled", "facePreset"];
const FIELDS = ["model", "prompt", "params", "n", "seconds", "size", "ratio", "aspect_ratio"].concat(PARAM_FIELDS, MEDIA_FIELDS);
const USAGE_SCHEMA = {
  requests: { type: "number", unit: "count", unitLabel: { en: "video", zh: "条" }, description: { en: "Video generation per-video price", zh: "视频按条单价" } },
  seconds: { type: "number", unit: "second", description: { en: "Video generation per-second price", zh: "视频按秒单价" } },
  resolution: { enum: RESOLUTIONS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
};

export const meta = {
  apiVersion: 1,
  key: "aggc",
  name: "AGGC 灵感之境",
  version: "1.0.0",
  author: { name: "jiayi-1994" },
  description: { en: "Video generation through AGGC with purchase-cost scheduling", zh: "通过灵感之境生成视频，支持采购成本调度" },
  icon: "text:AG",
  website: "https://aggc.site/docs",
  baseUrl: "https://aggc.site",
  models: Object.keys(MODELS),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: USAGE_SCHEMA,
  usageProfiles: Object.keys(MODELS).map(function (model) {
    const tiers = MODELS[model].tiers;
    const schema = { requests: USAGE_SCHEMA.requests, seconds: USAGE_SCHEMA.seconds };
    if (tiers.length) schema.resolution = { ...USAGE_SCHEMA.resolution, enum: tiers };
    return {
      models: [model], schema,
      examples: tiers.length ? tiers.map(function (tier) {
        return { label: tier + " · 5s", facts: { requests: 1, seconds: 5, resolution: tier } };
      }) : [{ label: "5s", facts: { requests: 1, seconds: 5 } }],
    };
  }),
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value) {
  return typeof value === "string" && value.length <= 8192 && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value);
}

function duration(value) {
  if ((typeof value !== "number" && typeof value !== "string") || !/^[1-9]\d*$/.test(String(value).trim())) {
    throw new Error("duration must be an integer number of seconds");
  }
  const seconds = Number(value);
  // The largest documented output is 30s, below MaxTaskDurationSeconds (3600).
  if (!Number.isSafeInteger(seconds) || seconds < 4 || seconds > 30) throw new Error("duration must be between 4 and 30 seconds");
  return seconds;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be a string");
  const tier = value.trim().toLowerCase() === "2160p" ? "4k" : value.trim().toLowerCase();
  if (!RESOLUTIONS.includes(tier)) throw new Error("unsupported resolution: " + value);
  return tier;
}

function ratio(value) {
  if (typeof value !== "string" || !RATIOS.includes(value.trim())) throw new Error("aspect ratio must be one of " + RATIOS.join(", "));
  return value.trim();
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

function mediaURLs(input, names) {
  let selected;
  for (const name of names) {
    if (!has(input, name)) continue;
    if (selected !== undefined) throw new Error("provide only one reference field: " + names.join("/"));
    let value = input[name];
    if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && value[0].trim().startsWith("[")) value = value[0];
    if (typeof value === "string" && value.trim().startsWith("[")) {
      try { value = JSON.parse(value); } catch (_error) { throw new Error(name + " must be a URL array"); }
    }
    if (name === "input_reference" && isObject(value)) {
      if (Object.keys(value).length !== 1 || !has(value, "image_url")) throw new Error("input_reference must contain only image_url");
      value = value.image_url;
    }
    if (typeof value === "string") value = [value];
    if (!Array.isArray(value) || value.length > 30) throw new Error(name + " must be a URL array of at most 30 items");
    selected = value.map(function (url) {
      if (typeof url !== "string" || !httpURL(url.trim())) throw new Error(name + " entries must be http(s) URLs without credentials");
      return url.trim();
    });
  }
  return selected || [];
}

// This same validator runs before quoting, reserving and submitting. Nothing in
// a parameter bag may silently override a duration, tier or reference list.
function videoRequest(value, model) {
  if (!isObject(value)) throw new Error("video request must be a JSON object");
  const input = {};
  for (const name of Object.keys(value)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
    if (name !== "params") input[name] = value[name];
  }
  if (has(value, "params")) {
    if (!isObject(value.params)) throw new Error("params must be an object");
    for (const name of Object.keys(value.params)) {
      if (!PARAM_FIELDS.includes(name)) throw new Error("unsupported params field: " + name);
      if (has(input, name)) throw new Error("duplicate video parameter: " + name);
      input[name] = value.params[name];
    }
  }
  if (has(input, "n") && input.n !== 1 && input.n !== "1") throw new Error("n must be 1");
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  const seconds = scalarAlias(input, ["seconds", "duration"], duration);
  if (seconds === undefined) throw new Error("seconds or duration is required");
  let tier = scalarAlias(input, ["resolution"], resolution);
  let aspectRatio = scalarAlias(input, ["aspectRatio", "ratio", "aspect_ratio"], ratio);
  if (has(input, "size")) {
    if (typeof input.size !== "string") throw new Error("size must be a resolution or WIDTHxHEIGHT");
    const text = input.size.trim().toLowerCase();
    let sizeTier;
    if (!text.includes("x")) sizeTier = resolution(text);
    else {
      const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(text);
      if (!match) throw new Error("size must be a supported WIDTHxHEIGHT");
      const width = Number(match[1]);
      const height = Number(match[2]);
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(function (candidate) {
        const parts = candidate.split(":");
        return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01;
      });
      if (!sizeRatio) throw new Error("size does not map to a supported aspect ratio");
      if (aspectRatio !== undefined && aspectRatio !== sizeRatio) throw new Error("size conflicts with aspect ratio");
      aspectRatio = sizeRatio;
    }
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    tier = sizeTier;
  }
  const params = { duration: seconds, aspectRatio: aspectRatio || "16:9" };
  if (tier !== undefined) params.resolution = tier;
  for (const names of MEDIA_GROUPS) {
    const urls = mediaURLs(input, names);
    if (urls.length) params[names[0]] = urls;
  }
  if (has(input, "faceModeEnabled")) {
    if (typeof input.faceModeEnabled !== "boolean") throw new Error("faceModeEnabled must be a boolean");
    params.faceModeEnabled = input.faceModeEnabled;
  }
  if (has(input, "facePreset")) {
    if (typeof input.facePreset !== "string" || !/^(blur|pixel|mask|grid|sketch|pencil|cutleft|sketchcut)\|(face|halfleft|halfright|eyes|eyeleft|eyeright|eyesmouth|tface)\|(rect|ellipse)$/.test(input.facePreset)) {
      throw new Error("unsupported facePreset");
    }
    if (params.faceModeEnabled === false) throw new Error("facePreset conflicts with faceModeEnabled=false");
    params.facePreset = input.facePreset;
  }
  if (model !== undefined) {
    if (!has(MODELS, model)) throw new Error("unsupported upstream video model: " + model);
    const limits = MODELS[model];
    if (seconds < limits.min || seconds > limits.max) throw new Error(model + " duration must be between " + limits.min + " and " + limits.max + " seconds");
    if (limits.tiers.length) {
      if (tier === undefined) throw new Error("resolution or size is required for " + model);
      if (!limits.tiers.includes(tier)) throw new Error("unsupported resolution for " + model + ": " + tier);
    } else if (tier !== undefined) throw new Error(model + " has no selectable resolution; omit resolution and size");
    for (const [field, limit] of [["imageUrls", limits.images], ["videoUrls", limits.videos], ["audioUrls", limits.audios]]) {
      if ((params[field] || []).length > limit) throw new Error(model + " allows at most " + limit + " " + field);
    }
    if (!limits.face && (has(params, "facePreset") || has(params, "faceModeEnabled"))) throw new Error("face options are not supported for " + model);
  }
  return { prompt: input.prompt, params };
}

function supportedModel(name) {
  const model = name === "minimax-h3" ? "aggc-minimax-h3" : name;
  if (typeof model !== "string" || !has(MODELS, model)) throw new Error("unsupported upstream video model: " + name);
  return model;
}

function modelRequest(ctx) {
  const model = supportedModel(ctx.upstreamModel || ctx.model);
  // The initial decoder accepts public aliases; every driver hook must have a
  // supported final machine identity after the host applies channel mapping.
  return { model_id: model === "aggc-minimax-h3" ? "minimax-h3" : model, type: "video", ...videoRequest(ctx.requestBody, model) };
}

function baseURL(ctx) {
  if (typeof ctx.baseUrl !== "string" || !/^https:\/\/[^\s/?#@\\]+(?:\/[^\s?#\\]*)?$/i.test(ctx.baseUrl)) {
    throw new Error("channel Base URL must use HTTPS without credentials, query or fragment");
  }
  return ctx.baseUrl.replace(/\/+$/, "");
}

function authHeaders(ctx) {
  const key = ctx.apiKey || ctx.authHeader;
  if (typeof key !== "string" || !key.trim() || /[\s\x00-\x1f\x7f]/.test(key.trim())) throw new Error("channel API key is required and must not contain whitespace");
  // AGGC uses the raw key, never a Bearer prefix, in this header only.
  return { "x-api-key": key.trim() };
}

function jobID(value) {
  if (typeof value === "number" && (!Number.isSafeInteger(value) || value <= 0)) throw new Error("invalid upstream job_id");
  if ((typeof value !== "number" && typeof value !== "string") || !/^[1-9]\d{0,19}$/.test(String(value))) throw new Error("invalid upstream job_id");
  return String(value);
}

export function buildSubmitRequest(ctx) {
  const body = modelRequest(ctx);
  // The catalog uses uppercase 768P on the wire; scheduling uses canonical 768p.
  if (body.params.resolution === "768p") body.params.resolution = "768P";
  return {
    url: baseURL(ctx) + "/api/v1/prot/generate", method: "POST",
    headers: { ...authHeaders(ctx), "Content-Type": "application/json" }, body,
  };
}

export function extractUsage(ctx) {
  const params = modelRequest(ctx).params;
  const facts = { requests: 1, seconds: params.duration };
  if (params.resolution !== undefined) facts.resolution = params.resolution;
  return facts;
}

export function extractUsageOnComplete() {
  // Query responses contain URLs and account credits, not verified output usage.
  // Preserve the reserved request facts; never bill credits_frozen/remaining.
  return {};
}

export function describeSpec(ctx) {
  const params = modelRequest(ctx).params;
  return {
    spec_version: 2,
    output_seconds: params.duration,
    seconds_kind: "exact",
    resolution: params.resolution || "*",
    references: { image: (params.imageUrls || []).length, video: (params.videoUrls || []).length, audio: (params.audioUrls || []).length },
    reference_video_urls: params.videoUrls || [],
  };
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || !Number.isInteger(body.code) || !isObject(body.data)) return { status: "UNKNOWN", reason: "invalid AGGC task response" };
  const data = body.data;
  if (ctx.taskId !== undefined) {
    try {
      if (jobID(data.job_id) !== jobID(ctx.taskId)) return { status: "UNKNOWN", reason: "upstream job_id does not match the queried task" };
    } catch (_error) { return { status: "UNKNOWN", reason: "invalid upstream job_id" }; }
  }
  const status = typeof data.status === "string" ? data.status.toLowerCase() : "";
  // Only the documented generation-failure code can terminate a task on a
  // nonzero envelope. Auth, balance and server errors are polling failures.
  if (body.code !== 0 && !(body.code === 3001 && status === "failed")) return { status: "UNKNOWN", reason: "AGGC query error: " + body.code };
  if (status === "failed") {
    const text = typeof data.error_message === "string" && data.error_message ? data.error_message : typeof body.message === "string" && body.message !== "OK" ? body.message : "video generation failed";
    return { status: "FAILURE", reason: (body.code === 3001 ? "aggc:3001: " : "") + text };
  }
  if (status === "success") {
    if (!httpURL(data.video_url)) return { status: "UNKNOWN", reason: "successful task has no usable video URL" };
    return { status: "SUCCESS" };
  }
  if (status === "processing") return { status: "IN_PROGRESS" };
  if (status === "queued" || status === "pending") return { status: "QUEUED" };
  return { status: "UNKNOWN", reason: "unrecognized AGGC task status: " + status };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body) || !Number.isInteger(body.code)) throw new Error("invalid AGGC create response");
  const data = isObject(body.data) ? body.data : {};
  const taskId = data.job_id == null ? undefined : jobID(data.job_id);
  const status = typeof data.status === "string" ? data.status.toLowerCase() : "";
  if (body.code !== 0 && !(body.code === 3001 && status === "failed" && taskId)) {
    if (taskId || (status && status !== "failed")) throw new Error("conflicting AGGC acceptance and rejection signals");
    // Unknown error codes are ambiguous; never encourage resubmission of work
    // that may have been accepted by an evolving provider API.
    if (![1001, 1002, 1003, 1004, 1005, 3001].includes(body.code)) throw new Error("unrecognized AGGC create error: " + body.code);
    const reason = typeof data.error_message === "string" && data.error_message ? data.error_message : typeof body.message === "string" && body.message ? body.message : "video request rejected";
    return { rejected: { reason: "aggc:" + body.code + ": " + reason } };
  }
  if (!taskId) throw new Error("upstream create response has no job_id");
  const result = parseTaskResult({}, body);
  const output = { taskId, taskData: body };
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  // The documented create success has no video_url. Persist its job_id and
  // query for the result instead of prematurely completing without an artifact.
  return output;
}

export function buildQueryRequest(ctx) {
  return { url: baseURL(ctx) + "/api/v1/prot/query/" + jobID(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && isObject(task.data) && isObject(task.data.data) && httpURL(task.data.data.video_url)
    ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video" || !isObject(ctx.data) || !isObject(ctx.data.data) || !httpURL(ctx.data.data.video_url)) throw new Error("artifact_not_found");
  // The host applies SSRF and redirect checks to credentialless content fetches.
  // Never forward x-api-key, Authorization or client headers to result storage.
  return { url: ctx.data.data.video_url, method: ctx.clientRequest.method, credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/^aggc:(1001|1002|1003|1004):/.test(text)) return "upstream";
  if (/timeout|timed out|service (is temporarily )?unavailable|internal (server )?error|quota (exhausted|exceeded)|insufficient (quota|balance)|rate limit|connection|authentication|permission denied|超时|服务(暂)?不可用|内部错误|(?:额度|余额|积分)(不足|耗尽)|限流/.test(text)) return "upstream";
  if (/cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/^aggc:(1005|3001):/.test(text)) return "user";
  if (/content policy violation|content violation|moderation (rejected|blocked)|content moderation rejected|审核(未通过|不通过|拒绝)|未通过(内容)?审核|内容.{0,12}(违规|敏感|不合规)|invalid (image|video|audio|prompt|url)\b/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      let value;
      if (body && body.kind === "json") value = body.value;
      else if (body && body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("file uploads are not supported; use public reference URLs");
        value = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(value, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) value[name] = entries;
          else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            value[name] = entries[0];
            if (name === "params" || name === "faceModeEnabled") {
              try { value[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be valid JSON"); }
            }
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(value) || typeof value.model !== "string" || !value.model.trim()) throw new Error("model is required");
      if (ctx.model && value.model !== ctx.model) throw new Error("model does not match the selected model");
      const selected = ctx.upstreamModel || value.model;
      let model;
      if (ctx.upstreamModel || has(MODELS, selected) || selected === "minimax-h3") model = supportedModel(selected);
      const requestBody = videoRequest(value, model);
      const params = requestBody.params;
      return { kind: "submit", model: value.model, action: params.imageUrls || params.videoUrls || params.audioUrls ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      if (task.status !== "SUCCESS" || !isObject(task.data) || !isObject(task.data.data)) return {};
      const data = task.data.data;
      const output = {};
      if (httpURL(data.video_url)) output.url = data.video_url;
      if (httpURL(data.video_cover_url)) output.video_cover_url = data.video_cover_url;
      // Exclude job_id, account balances and private provider error details.
      return output;
    },
  },
};
