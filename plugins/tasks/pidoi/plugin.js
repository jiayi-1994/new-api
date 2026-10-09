// Task Plugin API v1 for https://zizi.pidoi.com/docs/index.html.
// Billing units come from the user's Pidoi /api/pricing snapshot, not model prose.
// Prices belong to the host's saved expressions; this file only reports usage.
const MODELS = {
  "dola-seedance-2.5": { unit: "request", max: 30, resolutions: ["720p"], media: [30, 0, 10] },
  "sd-2.5-720p-pro": { unit: "second", min: 5, max: 30, resolutions: ["720p"], media: [30, 0, 10] },
  "Bt-sd2.0-720p": { unit: "second", resolutions: ["720p"], media: [9, 3, 3] },
  "wan30-1080p-fast": { unit: "second", min: 4, max: 30, resolutions: ["1080p"], media: [10, 5, 5], videoMax: 15 },
  "wan30-720p": { unit: "second", min: 4, max: 30, resolutions: ["720p"], media: [10, 5, 5], videoMax: 15, audioNeedsVisual: true },
  "seedace-2.0-480p": { unit: "second", min: 4, max: 15, resolutions: ["480p"], media: [9, 3, 3], referenceNeedsImage: true },
  "jydancan-1.5": { unit: "request" },
  "seedace-2.0-720p": { unit: "second", min: 4, max: 15, resolutions: ["720p"], media: [9, 3, 3], referenceNeedsImage: true },
  "sd-2.5-720p-3000": { unit: "request", resolutions: ["720p"], media: [30, 0, 0] },
  "sd-2.5-480p-plus": { unit: "second", min: 4, max: 30, resolutions: ["480p"], media: [30, 10, 10] },
  "sd-2.5-720p-ultra-cf": { unit: "second", resolutions: ["720p"] },
  "sora-v3-933-pro": { unit: "request", fixed: 15, resolutions: ["720p"], media: [9, 3, 3], totalMedia: 12 },
  "sd-2.5-720p-plus": { unit: "second", min: 4, max: 30, resolutions: ["720p"], media: [30, 10, 10] },
  "seedace-2.5-480p": { unit: "second", resolutions: ["480p"] },
  "sora-v3-933-plus": { unit: "request", fixed: 15, resolutions: ["720p"], media: [9, 3, 3], totalMedia: 12 },
  "veo-3.1-fast": { unit: "request" },
  "sd2-pro-933-480p": { unit: "request", max: 15, resolutions: ["480p"], media: [9, 3, 3] },
  "tejiasd-mini-720p": { unit: "request", max: 15, resolutions: ["480p", "720p"], media: [9, 0, 3] },
  "sd-2.5-1080p-max": { unit: "second", resolutions: ["1080p"] },
  "jiuyue111": { unit: "request", min: 5, max: 15, resolutions: ["720p"], media: [9, 0, 3] },
  "tejiasd": { unit: "request", fixed: 15, resolutions: ["720p"], media: [9, 3, 3] },
  "sd-2.5-720p-900": { unit: "request", max: 30, resolutions: ["720p"], media: [9, 0, 0], promptMax: 12000 },
  "seedace-2.5-720p": { unit: "second", resolutions: ["720p"] },
  "sd-2.5-480p-pro": { unit: "second", min: 5, max: 30, resolutions: ["480p"], media: [30, 0, 10] },
  "sora-933-720P-fast": { unit: "request", durations: [10, 15], resolutions: ["720p"] },
  "H3video-2k": { unit: "request", fixed: 15, resolutions: ["768p"], media: [9, 3, 3] },
  "sd-2.5-720p-ultra": { unit: "second", resolutions: ["720p"] },
  "Bt-sd2.0-480p": { unit: "second", resolutions: ["480p"], media: [9, 3, 3] },
};
// Unpublished capabilities remain upstream-validated. These are host safety bounds,
// not a promise that an undocumented model supports hour-long videos or 50 assets.
const MAX_TASK_DURATION_SECONDS = 3600; // relay/common.MaxTaskDurationSeconds
const MAX_MEDIA = 50;
const RESOLUTIONS = ["480p", "720p", "768p", "1080p", "1440p", "4k"];
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
const IMAGE_LISTS = ["images", "image_urls", "referenceImages"];
const IMAGE_EXTRAS = ["reference_image_urls", "reference_images"];
const IMAGE_PRIMARY = ["image_url", "image", "input_reference"];
const VIDEO_FIELDS = ["reference_video", "reference_videos", "videos", "video_url", "video_urls", "input_video", "referenceVideos"];
const AUDIO_FIELDS = ["audio_url", "audio_urls", "audios", "input_audio", "referenceAudios", "reference_audios"];
const MEDIA_FIELDS = IMAGE_LISTS.concat(IMAGE_EXTRAS, IMAGE_PRIMARY, VIDEO_FIELDS, AUDIO_FIELDS);
const UNSUPPORTED_MEDIA_FIELDS = ["media", "input", "parameters", "first_image_url", "last_image_url", "first_image", "last_image"];
const FIELDS = ["model", "prompt", "seconds", "duration", "resolution", "size", "aspect_ratio", "aspectRatio", "ratio"].concat(MEDIA_FIELDS);
const REQUEST_SCHEMA = {
  requests: { type: "number", unit: "count", description: { en: "Video generation unit price", zh: "视频生成单价" } },
};
const SECOND_SCHEMA = {
  seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
};

export const meta = {
  apiVersion: 1,
  key: "pidoi",
  name: "Pidoi Video",
  version: "1.0.5",
  author: { name: "jiayi-1994" },
  description: { en: "Pidoi video generation with per-request or per-second pricing by model", zh: "通过 Pidoi 生成视频，按模型分别按次或按秒计费" },
  icon: "text:PI",
  baseUrl: "https://pidoi.com",
  models: Object.keys(MODELS),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: REQUEST_SCHEMA,
  usageExamples: [{ label: "1 video", facts: { requests: 1 } }],
  usageProfiles: [{
    models: Object.keys(MODELS).filter(function (name) { return MODELS[name].unit === "second"; }),
    schema: SECOND_SCHEMA,
    examples: [{ label: "10s", facts: { seconds: 10 } }, { label: "15s", facts: { seconds: 15 } }],
  }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function seconds(value) {
  if ((typeof value !== "number" && typeof value !== "string") || !/^[1-9]\d*$/.test(String(value).trim())) {
    throw new Error("seconds must be a positive integer");
  }
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number > MAX_TASK_DURATION_SECONDS) throw new Error("seconds must be between 1 and " + MAX_TASK_DURATION_SECONDS);
  return number;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be a string");
  const text = value.trim().toLowerCase();
  const normalized = text === "2160p" ? "4k" : text;
  if (!RESOLUTIONS.includes(normalized)) throw new Error("unsupported resolution: " + value);
  return normalized;
}

function ratio(value) {
  if (typeof value !== "string" || !RATIOS.includes(value.trim())) throw new Error("unsupported aspect_ratio");
  return value.trim();
}

function scalarAlias(value, names, parse) {
  let selected;
  for (const name of names) {
    if (!has(value, name)) continue;
    const current = parse(value[name]);
    if (selected !== undefined && selected !== current) throw new Error("conflicting fields: " + names.join("/"));
    selected = current;
  }
  return selected;
}

function referenceURLs(value) {
  if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && /^[\[{]/.test(value[0].trim())) value = value[0];
  if (typeof value === "string" && /^[\[{]/.test(value.trim())) {
    try { value = JSON.parse(value); } catch (_error) { throw new Error("reference must be a URL or JSON URL array"); }
  }
  if (isObject(value) && has(value, "image_url")) value = value.image_url;
  if (typeof value === "string") value = [value];
  if (!Array.isArray(value) || value.length > MAX_MEDIA) throw new Error("reference must be a URL array of at most " + MAX_MEDIA + " items");
  return value.map(function (url) {
    if (typeof url !== "string" || !/^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(url.trim())) {
      throw new Error("references require public http(s) URLs without embedded credentials");
    }
    return url.trim();
  });
}

// Preserve order and duplicates inside a list; equal aliases must not double it.
function mediaAliases(value, names) {
  let selected;
  for (const name of names) {
    if (!has(value, name)) continue;
    const current = referenceURLs(value[name]);
    if (selected !== undefined && JSON.stringify(selected) !== JSON.stringify(current)) throw new Error("conflicting reference fields: " + names.join("/"));
    selected = current;
  }
  return selected;
}

function videoParams(value) {
  if (!isObject(value)) throw new Error("video request must be an object");
  for (const name of UNSUPPORTED_MEDIA_FIELDS) {
    if (has(value, name)) throw new Error(name + " is not supported; use flat reference URL fields");
  }
  if (typeof value.prompt !== "string" || !value.prompt.trim()) throw new Error("prompt is required");
  const body = { prompt: value.prompt };
  const duration = scalarAlias(value, ["seconds", "duration"], seconds);
  if (duration !== undefined) body.seconds = duration;
  if (has(value, "resolution")) body.resolution = resolution(value.resolution);
  const selectedRatio = scalarAlias(value, ["aspect_ratio", "aspectRatio", "ratio"], ratio);
  if (selectedRatio !== undefined) body.aspect_ratio = selectedRatio;
  if (has(value, "size")) {
    if (typeof value.size !== "string") throw new Error("size must be WIDTHxHEIGHT, an aspect ratio or a resolution");
    const text = value.size.trim().toLowerCase();
    const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(text);
    let selectedResolution;
    let sizeRatio;
    if (match) {
      const width = Number(match[1]);
      const height = Number(match[2]);
      selectedResolution = resolution(Math.min(width, height) + "p");
      for (const name of RATIOS) {
        const parts = name.split(":");
        if (Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01) sizeRatio = name;
      }
      if (!sizeRatio) throw new Error("size does not map to a supported aspect ratio");
    } else if (text.includes(":")) sizeRatio = ratio(text);
    else selectedResolution = resolution(text);
    if (selectedResolution) {
      if (has(body, "resolution") && body.resolution !== selectedResolution) throw new Error("size conflicts with resolution");
      body.resolution = selectedResolution;
    }
    if (sizeRatio) {
      if (has(body, "aspect_ratio") && body.aspect_ratio !== sizeRatio) throw new Error("size conflicts with aspect_ratio");
      body.aspect_ratio = sizeRatio;
    }
  }

  const primary = mediaAliases(value, IMAGE_PRIMARY);
  if (primary && primary.length > 1) throw new Error("image_url/input_reference accepts only one primary image");
  const extra = mediaAliases(value, IMAGE_EXTRAS);
  let images = mediaAliases(value, IMAGE_LISTS);
  if (primary !== undefined || extra !== undefined) {
    const combined = (primary || []).concat(extra || []);
    if (images !== undefined && JSON.stringify(images) !== JSON.stringify(combined)) throw new Error("images conflicts with image_url/reference_image_urls");
    images = combined;
  }
  const videos = mediaAliases(value, VIDEO_FIELDS) || [];
  const audios = mediaAliases(value, AUDIO_FIELDS) || [];
  images = images || [];
  if (images.length + videos.length + audios.length > MAX_MEDIA) throw new Error("at most " + MAX_MEDIA + " references are allowed");
  if (images.length) {
    body.image_url = images[0];
    if (images.length > 1) body.reference_image_urls = images.slice(1);
  }
  if (videos.length) body.reference_videos = videos;
  if (audios.length) body.audio_urls = audios;
  return body;
}

function modelRequest(ctx) {
  const name = ctx.upstreamModel || ctx.model;
  if (!has(MODELS, name)) throw new Error("unsupported Pidoi video model: " + name);
  // Plugin-priced mappings must keep their billing unit. A host-frozen video
  // sale uses its own request facts and ignores both models' plugin pricing.
  if (ctx.salesSource !== "video_request" && has(MODELS, ctx.model) && MODELS[ctx.model].unit !== MODELS[name].unit) throw new Error("model mapping cannot change billing unit");
  const model = MODELS[name];
  const body = videoParams(ctx.requestBody);
  if (model.fixed && !has(body, "seconds")) body.seconds = model.fixed;
  if (!has(body, "seconds")) throw new Error("seconds or duration is required");
  if (model.fixed && body.seconds !== model.fixed) throw new Error(name + " requires " + model.fixed + " seconds");
  if (body.seconds < (model.min || 1) || body.seconds > (model.max || MAX_TASK_DURATION_SECONDS)) throw new Error(name + " seconds outside supported range");
  if (model.durations && !model.durations.includes(body.seconds)) throw new Error(name + " requires " + model.durations.join(" or ") + " seconds");
  if (model.resolutions) {
    if (!has(body, "resolution") && model.resolutions.length === 1) body.resolution = model.resolutions[0];
    if (!model.resolutions.includes(body.resolution)) throw new Error(name + " resolution must be " + model.resolutions.join(" or "));
  }
  if (name === "tejiasd-mini-720p" && body.resolution === "720p" && body.seconds > 12) throw new Error(name + " 720p supports at most 12 seconds");
  if (model.promptMax && Array.from(body.prompt).length > model.promptMax) throw new Error(name + " prompt is too long");
  const images = (body.image_url ? 1 : 0) + (body.reference_image_urls || []).length;
  const videos = (body.reference_videos || []).length;
  const audios = (body.audio_urls || []).length;
  if (model.media && (images > model.media[0] || videos > model.media[1] || audios > model.media[2])) throw new Error(name + " reference limits are " + model.media.join("/") + " images/videos/audios");
  if (model.totalMedia && images + videos + audios > model.totalMedia) throw new Error(name + " allows at most " + model.totalMedia + " total references");
  if (model.videoMax && videos && body.seconds > model.videoMax) throw new Error(name + " with reference video supports at most " + model.videoMax + " seconds");
  if (model.referenceNeedsImage && (videos || audios) && !images) throw new Error(name + " video/audio references require an image");
  if (model.audioNeedsVisual && audios && !images && !videos) throw new Error(name + " audio references require an image or video");
  return Object.assign({ model: name }, body);
}

function baseURL(ctx) {
  if (typeof ctx.baseUrl !== "string" || !/^https?:\/\/[^\s/?#@\\]+(?:\/[^\s?#\\]*)?$/i.test(ctx.baseUrl)) throw new Error("channel Base URL must be an http(s) URL without query, fragment or credentials");
  const base = ctx.baseUrl.replace(/\/+$/, "");
  if (/\/v1$/i.test(base)) throw new Error("channel Base URL must not end with /v1");
  return base;
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string") throw new Error("channel API key is required");
  const token = credential.trim().replace(/^Bearer\s+/i, "").trim();
  if (!token || /\s/.test(token)) throw new Error("channel API key is required and must not contain whitespace");
  return { Authorization: "Bearer " + token };
}

function taskFailure(body) {
  if (isObject(body.error) && typeof body.error.message === "string" && body.error.message) return body.error.message;
  if (typeof body.error === "string" && body.error) return body.error;
  if (typeof body.message === "string" && body.message) return body.message;
  return "Pidoi video generation failed";
}

function taskResult(body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "invalid task response" };
  const raw = typeof body.status === "string" ? body.status.trim().toLowerCase() : "";
  const statuses = { queued: "QUEUED", processing: "IN_PROGRESS", in_progress: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE" };
  if (!has(statuses, raw)) return { status: "UNKNOWN", reason: "unrecognized Pidoi task status: " + raw };
  if (raw !== "failed" && (body.error || body.success === false)) return { status: "UNKNOWN", reason: taskFailure(body) };
  const result = { status: statuses[raw] };
  if (raw === "failed") result.reason = taskFailure(body);
  if (typeof body.progress === "number" && Number.isFinite(body.progress) && body.progress >= 0 && body.progress <= 100) result.progress = body.progress + "%";
  return result;
}

export function buildSubmitRequest(ctx) {
  const body = modelRequest(ctx);
  body.seconds = String(body.seconds); // Pidoi's recommended wire representation.
  return { url: baseURL(ctx) + "/v1/videos", method: "POST", headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json; charset=utf-8" }), body };
}

export function extractUsage(ctx) {
  const body = modelRequest(ctx);
  return MODELS[body.model].unit === "second" ? { seconds: body.seconds } : { requests: 1 };
}

// Scheduling facts for the host's purchase-cost quote; never billing usage.
// Per-request billing does not make the spec unknown: tejiasd-mini-720p still
// reports its 480p/720p tier. Only models without published resolutions are "*".
export function describeSpec(ctx) {
  const body = modelRequest(ctx);
  const model = MODELS[body.model];
  return {
    spec_version: 2,
    reference_video_urls: body.reference_videos || [],
    output_seconds: body.seconds,
    seconds_kind: model.fixed ? "fixed" : "exact",
    resolution: model.resolutions ? body.resolution : "*",
    references: {
      video: (body.reference_videos || []).length,
      image: (body.image_url ? 1 : 0) + (body.reference_image_urls || []).length,
      audio: (body.audio_urls || []).length,
    },
  };
}

// classifyFailure attributes a terminal failure reason for channel health:
// content or input rejections are the user's, cancellations are neutral, and
// anything else counts against the upstream channel.
export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  // Only the marker this plugin writes for a cancelled status, or an explicit
  // user cancellation, is neutral; a provider-side cancellation is upstream.
  if (/^cancelled: |cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  // Mentioning moderation or validation infrastructure is not a user rejection.
  // Keep service failures upstream even if their message contains these words.
  if (/timeout|timed out|service (is temporarily )?unavailable|internal (server )?error|quota (exhausted|exceeded)|insufficient (quota|balance)|rate limit|connection|authentication|permission denied|超时|服务(暂)?不可用|内部错误|额度(不足|耗尽)|限流/.test(text)) return "upstream";
  if (/content policy violation|content violation|violates (the )?(content|usage) polic|moderation (rejected|blocked)|rejected by (the )?(content )?moderation|审核(未通过|不通过|拒绝)|未通过(内容)?审核|内容.{0,12}(违规|敏感|不合规)|(提示词|素材|图片|视频|输入文本).{0,12}(敏感|违规)|触发敏感词|invalid (image|video|audio|prompt|url)\b|unsupported (image|video|audio) (format|type)\b/.test(text)) return "user";
  // Observed provider refusals; numeric error codes alone do not prove user fault.
  if (/请求参数或素材格式不符合要求|输出视频可能涉及版权限制/.test(text)) return "user";
  return "upstream";
}

export function extractUsageOnComplete() {
  // The documented response seconds echoes the request, not measured duration.
  // Keep frozen validated submission facts; upstream money/token fields are not usage.
  return {};
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("upstream create response must be an object");
  const result = taskResult(body);
  const ids = [body.task_id, body.id];
  if (ids.some(value => value != null && typeof value !== "string")) throw new Error("upstream create response has an invalid task id");
  const id = ids.find(value => typeof value === "string" && value.trim());
  if ((body.error || body.success === false) && result.status !== "FAILURE") {
    const acceptedStatus = [body.status].some(value => value != null && String(value).trim() !== "" && !/^(failed|failure|cancelled|canceled)(?:[:：].*)?$/i.test(String(value).trim()));
    if (id || acceptedStatus) throw new Error("upstream create response has conflicting acceptance and rejection signals");
    return { rejected: { reason: taskFailure(body) } };
  }
  if (typeof id !== "string" || !id.trim()) {
    if (result.status === "FAILURE") return { rejected: { reason: taskFailure(body) } };
    throw new Error("upstream create response has no task id");
  }
  const output = { taskId: id.trim(), taskData: body };
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId.trim()) throw new Error("upstream task id is required");
  return { url: baseURL(ctx) + "/v1/videos/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(_ctx, body) {
  return taskResult(body);
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  if (typeof ctx.upstreamTaskId !== "string" || !ctx.upstreamTaskId.trim()) throw new Error("upstream task id is required");
  // The documented video_url is an authenticated Pidoi endpoint, not a public CDN.
  // Reconstruct it from the channel and persisted ID; never attach keys to result URLs.
  return {
    url: baseURL(ctx) + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId) + "/content",
    method: ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET",
    headers: authHeaders(ctx),
  };
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      if (!body || !["json", "multipart"].includes(body.kind)) throw new Error("JSON or multipart text fields are required");
      let value = body.value;
      if (body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("file uploads are not supported; send public media URLs");
        value = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (UNSUPPORTED_MEDIA_FIELDS.includes(name)) throw new Error(name + " is not supported; use flat reference URL fields");
          if (!FIELDS.includes(name)) continue;
          if (has(value, name)) throw new Error("duplicate field: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) { value[name] = entries; continue; }
          if (entries.length !== 1) throw new Error(name + " must be provided once");
          value[name] = entries[0];
        }
      }
      if (!isObject(value) || typeof value.model !== "string" || !value.model.trim()) throw new Error("model is required");
      const model = value.model.trim();
      if (ctx.model && model !== ctx.model) throw new Error("model does not match selected channel model");
      const requestBody = videoParams(value);
      // Channel mapping is unavailable in the initial candidate decode. Validate
      // model constraints after mapping, in both usage and submit hooks.
      if (ctx.upstreamModel) modelRequest({ model, upstreamModel: ctx.upstreamModel, requestBody });
      const references = requestBody.image_url || requestBody.reference_videos || requestBody.audio_urls;
      return { kind: "submit", model, action: references ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const data = isObject(task.data) ? task.data : {};
      const output = {};
      if (data.seconds !== undefined) {
        try { output.seconds = String(seconds(data.seconds)); } catch (_error) { /* invalid upstream metadata is not public usage */ }
      }
      for (const name of ["size", "resolution", "aspect_ratio"]) {
        if (typeof data[name] === "string") output[name] = data[name];
      }
      if (task.status === "FAILURE") output.error = { message: task.fail_reason || taskFailure(data) };
      // Host owns public ID/status/model and artifact URLs. Do not leak upstream IDs,
      // authenticated video_url, raw usage, metadata or private billing details.
      return output;
    },
  },
};
