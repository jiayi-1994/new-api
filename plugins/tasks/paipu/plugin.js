// Task Plugin API v1. Request contract verified against Paipu's public model
// catalog: https://api.paipu.net/api/media-models/catalog?surface=docs (2026-09-28).
//
// Public pricing + model catalog snapshot, 2026-09-28. Arrays list verified
// output resolutions, not prices. [] means the provider publishes no resolution
// spec: expose count/time pricing without inventing a resolution billing tier.
const MODELS = {
  "lec-gt-seedance-2-0-mini": ["480p", "720p"],
  "lec-gt-seedance-2-0-full": ["480p", "720p", "1080p"],
  "lec-gt-seedance-2-5-720p": ["480p", "720p", "1080p"],
  "lec-seedance-2-0-mini-c4-480p": ["480p"],
  "lec-seedance-2-0": ["720p"],
  "lec-seedance-2-0-933-stable": ["480p", "720p"],
  "lec-seedance-2-0-933-stable-edit": ["480p", "720p"],
  "lec-seedance-2-0-full-933-720p": ["720p"],
  "lec-seedance-2-0-fast-933-720p": ["720p"],
  "lec-ac-seedance-2-0-fast-2-720p": ["720p"],
  "lec-ac-seedance-2-5-10-image": [],
  "lec-brx-seedance-2-5-3010": ["720p"],
  "lec-bk-video-30s": ["720p"],
  "lec-h3video-2k": ["1440p"],
  "lec-haya-seedance-2-0-xg-720": ["720p"],
  "lec-haya-seedance-2-5-xg-720": ["720p"],
  "lec-md-seedance-2-0-900-720p": ["720p"],
  "lec-md-seedance-2-0-fast-900-720p": ["720p"],
  "lec-md-seedance-2-5-900-720p": ["720p"],
  "lec-mj-seedance-2-0-full-933-jd": ["720p"],
  "lec-mj-seedance-2-5-bd-720p": ["720p"],
  "lec-mj-seedance-2-5-vd-720p": ["720p"],
  "lec-mj-wan-3-0-1080p": ["720p", "1080p"],
  "lec-minimax-h3": ["720p"],
  "lec-minimax-h3-768p": ["768p"],
  "lec-rs-seedance-2-0-fast": ["720p"],
  "lec-seed-2-0-900": [],
  "lec-seed-2-5-900": [],
  "lec-ty-seedance-2-0-full-933-me-720p": ["720p"],
  "lec-ty-seedance-2-0-mini-933-j-480p": ["480p"],
  "lec-ty-seedance-2-0-mini-933-j-720p": ["720p"],
  "lec-ty-wan-3-0-1055-720p": ["720p"],
  "lec-ty-wan-3-0-1055-1080p": ["1080p"],
  "lec-vg-seedance-2-5-wd": ["720p"],
  "lec-vp-seedance-2-0-933-s2": ["720p"],
  "lec-vp-seedance-2-5-m2": ["480p", "720p", "1080p"],
  "lec-vp-wan-3-0-prime": ["720p", "1080p"],
  "lec-wan3-720p": ["480p", "720p", "1080p"],
  "lec-yu25-grok-video-1-5-preview": ["480p", "720p"],
};
// These APIs have no duration request field. Bill their actual fixed length,
// reject conflicting client values, and omit duration when submitting upstream.
const FIXED_DURATIONS = {
  "lec-ac-seedance-2-0-fast-2-720p": 15,
  "lec-ac-seedance-2-5-10-image": 30,
  "lec-brx-seedance-2-5-3010": 30,
  "lec-h3video-2k": 15,
  "lec-bk-video-30s": 30,
  "lec-seed-2-0-900": 15,
  "lec-seed-2-5-900": 30,
  "lec-md-seedance-2-5-900-720p": 30,
  "lec-mj-seedance-2-5-vd-720p": 30,
  "lec-mj-wan-3-0-1080p": 30,
};
// Keep the existing variable template's pricing schema unchanged.
const RESOLUTIONS = ["480p", "720p", "1080p"];
const ALL_RESOLUTIONS = ["480p", "720p", "768p", "1080p", "1440p"];
// Task Plugin has no imports from Go; mirror relay/common.MaxTaskDurationSeconds.
const MAX_TASK_DURATION_SECONDS = 3600;
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
const MEDIA_GROUPS = [
  ["images", "referenceImages", "reference_images", "image", "input_reference"],
  ["videos", "referenceVideos", "reference_videos"],
  ["audios", "referenceAudios", "reference_audios"],
];
const MEDIA_FIELDS = MEDIA_GROUPS.flat();
const FIELDS = ["model", "prompt", "duration", "seconds", "aspect_ratio", "ratio", "resolution", "size"].concat(MEDIA_FIELDS);
const USAGE_FIELDS = {
  requests: { type: "number", unit: "count", unitLabel: { en: "video", zh: "条" }, description: { en: "Video generation per-video price", zh: "视频按条单价" } },
  seconds: { type: "number", unit: "second", description: { en: "Video generation per-second price", zh: "视频按秒单价" } },
  resolution: { enum: RESOLUTIONS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
};
// Template name → fixed resolution. A fixed template is for vendor models whose
// API takes no resolution field: the fact is fixed and the field is omitted.
const TEMPLATES = { "paipu-video": null };
for (const fixed of ALL_RESOLUTIONS) TEMPLATES["paipu-video-" + fixed] = fixed;

const PROFILES = Object.keys(MODELS).concat(Object.keys(TEMPLATES)).map(function (name) {
  const resolutions = Object.prototype.hasOwnProperty.call(MODELS, name) ? MODELS[name] : TEMPLATES[name] ? [TEMPLATES[name]] : RESOLUTIONS;
  const schema = { requests: USAGE_FIELDS.requests, seconds: USAGE_FIELDS.seconds };
  if (resolutions.length) schema.resolution = { ...USAGE_FIELDS.resolution, enum: resolutions };
  const seconds = FIXED_DURATIONS[name] || (has(MODELS, name) ? 10 : 5);
  return {
    models: [name],
    schema,
    examples: resolutions.length ? resolutions.map(function (value) {
      return { label: value + " · " + seconds + "s", facts: { requests: 1, seconds, resolution: value } };
    }) : [{ label: seconds + "s", facts: { requests: 1, seconds } }],
  };
});

export const meta = {
  apiVersion: 1,
  key: "paipu",
  name: "Paipu Video",
  version: "2.2.0",
  author: { name: "jiayi-1994" },
  description: { en: "Paipu video generation; choose per-second, per-video or per-resolution pricing on each model's price page", zh: "通过 Paipu 生成视频，在各模型定价页自选按秒、按条或分辨率按条计费" },
  icon: "text:PP",
  baseUrl: "https://api.paipu.net",
  models: Object.keys(MODELS).concat(Object.keys(TEMPLATES)),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: USAGE_FIELDS,
  usageExamples: RESOLUTIONS.map(function (value) {
    return { label: value + " · 5s", facts: { requests: 1, seconds: 5, resolution: value } };
  }),
  usageProfiles: PROFILES,
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value, httpsOnly) {
  return typeof value === "string" && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value.trim()) &&
    (!httpsOnly || /^https:\/\//i.test(value.trim()));
}

function duration(value) {
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+$/.test(String(value).trim())) {
    throw new Error("duration must be an integer number of seconds");
  }
  const seconds = Number(value);
  if (!Number.isSafeInteger(seconds) || seconds < 1 || seconds > MAX_TASK_DURATION_SECONDS) {
    throw new Error("duration must be between 1 and " + MAX_TASK_DURATION_SECONDS + " seconds");
  }
  return seconds;
}

function ratio(value) {
  if (typeof value !== "string" || !RATIOS.includes(value.trim())) throw new Error("aspect_ratio must be one of " + RATIOS.join(", "));
  return value.trim();
}

function resolution(value) {
  if (typeof value !== "string" || !ALL_RESOLUTIONS.includes(value.trim().toLowerCase())) throw new Error("resolution must be " + ALL_RESOLUTIONS.join(", "));
  return value.trim().toLowerCase();
}

function referenceURLs(value, field) {
  if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && value[0].trim().startsWith("[")) value = value[0];
  if (typeof value === "string" && value.trim().startsWith("[")) {
    try { value = JSON.parse(value); } catch (_error) { throw new Error(field + " must contain a valid JSON URL array"); }
  }
  if (field === "input_reference" && isObject(value)) {
    if (Object.keys(value).length !== 1 || !has(value, "image_url")) throw new Error("input_reference must contain only image_url");
    value = value.image_url;
  }
  if (typeof value === "string") value = [value];
  if (!Array.isArray(value) || value.length > 30) throw new Error(field + " must be a URL array of at most 30 items");
  return value.map(function (url) {
    if (!httpURL(url, true)) throw new Error(field + " entries must be HTTPS URLs without embedded credentials");
    return url.trim();
  });
}

// Template-dependent checks stay out of initial decoding: channel mapping may
// select the template later. Billing and submission share modelRequest.
function videoParams(value) {
  if (!isObject(value)) throw new Error("video request must be an object");
  for (const name of Object.keys(value)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video request field: " + name);
  }
  if (typeof value.prompt !== "string" || !value.prompt.trim()) throw new Error("prompt is required");
  const output = { prompt: value.prompt };
  for (const name of ["duration", "seconds"]) {
    if (!has(value, name)) continue;
    const seconds = duration(value[name]);
    if (has(output, "duration") && output.duration !== seconds) throw new Error("seconds conflicts with duration");
    output.duration = seconds;
  }
  for (const name of ["aspect_ratio", "ratio"]) {
    if (!has(value, name)) continue;
    const selected = ratio(value[name]);
    if (has(output, "aspect_ratio") && output.aspect_ratio !== selected) throw new Error("ratio conflicts with aspect_ratio");
    output.aspect_ratio = selected;
  }
  if (has(value, "resolution")) output.resolution = resolution(value.resolution);
  if (has(value, "size")) {
    if (typeof value.size !== "string") throw new Error("size must be a resolution or WIDTHxHEIGHT");
    const text = value.size.trim().toLowerCase();
    const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(text);
    let selectedResolution;
    let selectedRatio;
    if (match) {
      const width = Number(match[1]);
      const height = Number(match[2]);
      selectedResolution = resolution(Math.min(width, height) + "p");
      selectedRatio = RATIOS.find(function (name) {
        const parts = name.split(":");
        const aspect = Number(parts[0]) / Number(parts[1]);
        // Permit integer rounding (854x480), not arbitrary approximate ratios.
        return Math.abs(width - height * aspect) <= 1;
      });
      if (!selectedRatio) throw new Error("size does not map to a supported aspect_ratio");
    } else {
      selectedResolution = resolution(text);
    }
    if (has(output, "resolution") && output.resolution !== selectedResolution) throw new Error("size conflicts with resolution");
    if (selectedRatio && has(output, "aspect_ratio") && output.aspect_ratio !== selectedRatio) throw new Error("size conflicts with aspect_ratio");
    output.resolution = selectedResolution;
    if (selectedRatio) output.aspect_ratio = selectedRatio;
  }
  for (const group of MEDIA_GROUPS) {
    const names = group.filter(function (name) { return has(value, name); });
    if (names.length > 1) throw new Error("provide only one reference field: " + group.join(", "));
    if (names.length) output[group[0]] = referenceURLs(value[names[0]], names[0]);
  }
  return output;
}

function modelRequest(ctx) {
  const name = ctx.upstreamModel || ctx.model;
  const isTemplate = has(TEMPLATES, name);
  if (!isTemplate && !has(MODELS, name)) throw new Error("unsupported upstream video model: map it to one of " + Object.keys(TEMPLATES).join(", "));
  const vendorModel = isTemplate ? (typeof ctx.model === "string" ? ctx.model.replace(/^paipu\//, "") : "") : name;
  if (!/^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,199}$/.test(vendorModel) || has(TEMPLATES, vendorModel)) {
    throw new Error("map a real Paipu model name to a video template; do not call the template directly");
  }
  const body = videoParams(ctx.requestBody);
  const fixedDuration = has(FIXED_DURATIONS, vendorModel) ? FIXED_DURATIONS[vendorModel] : null;
  if (fixedDuration) {
    if (has(body, "duration") && body.duration !== fixedDuration) throw new Error(vendorModel + " only supports " + fixedDuration + " seconds");
    body.duration = fixedDuration;
  }
  // The price page may bill by seconds, so the billed duration must be explicit.
  if (!has(body, "duration")) throw new Error("duration is required");
  const known = has(MODELS, vendorModel);
  const resolutions = known ? MODELS[vendorModel] : TEMPLATES[name] ? [TEMPLATES[name]] : RESOLUTIONS;
  const templateResolution = isTemplate ? TEMPLATES[name] : null;
  if (templateResolution) {
    if (!resolutions.includes(templateResolution)) throw new Error(vendorModel + " does not support the selected template resolution");
    if (has(body, "resolution") && body.resolution !== templateResolution) throw new Error(name + " only supports " + templateResolution);
    body.resolution = templateResolution;
  }
  const fixed = known ? resolutions.length === 1 ? resolutions[0] : null : templateResolution;
  if (fixed) {
    if (has(body, "resolution") && body.resolution !== fixed) throw new Error(name + " only supports " + fixed);
    delete body.resolution;
  } else if (!resolutions.length) {
    if (has(body, "resolution")) throw new Error(name + " has no published resolution parameter");
  } else if (!has(body, "resolution")) {
    throw new Error("resolution is required for " + name);
  } else if (!resolutions.includes(body.resolution)) {
    throw new Error(name + " only supports " + resolutions.join(", "));
  }
  if (isTemplate && !templateResolution && !RESOLUTIONS.includes(fixed || body.resolution)) {
    throw new Error("use the declared model or a matching fixed-resolution template for " + vendorModel);
  }
  // Provider-specific limits stay upstream-owned. URL and quantity bounds in
  // videoParams still protect the host before any billing occurs.
  body.model = vendorModel;
  return body;
}

function baseURL(ctx) {
  if (typeof ctx.baseUrl !== "string" || !/^https?:\/\/[^\s/?#@\\]+(?:\/[^\s?#\\]*)?$/i.test(ctx.baseUrl)) {
    throw new Error("channel Base URL must be an http(s) address without query, fragment or credentials");
  }
  const base = ctx.baseUrl.replace(/\/+$/, "");
  if (/\/v1$/i.test(base)) throw new Error("channel Base URL must not end with /v1");
  return base;
}

function authHeaders(ctx) {
  // For ordinary vendor API keys the official host supplies authHeader as the
  // raw key, while some credential resolvers supply a complete Bearer header.
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string" || !credential.trim()) throw new Error("channel API key is required");
  const token = credential.trim().replace(/^Bearer\s+/i, "").trim();
  if (!token || /\s/.test(token)) throw new Error("channel API key is required and must not contain whitespace");
  return { Authorization: "Bearer " + token };
}

function taskFailure(body) {
  if (isObject(body.error) && typeof body.error.message === "string" && body.error.message) return body.error.message;
  if (typeof body.error === "string" && body.error) return body.error;
  if (typeof body.message === "string" && body.message) return body.message;
  return "Paipu video generation failed";
}

function taskResult(body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "upstream task response must be an object" };
  const raw = typeof body.status === "string" ? body.status.trim().toLowerCase() : "";
  const statuses = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE" };
  if (!has(statuses, raw)) return { status: "UNKNOWN", reason: "unrecognized Paipu task status: " + raw };
  if (raw !== "failed" && (body.error || body.success === false)) return { status: "UNKNOWN", reason: taskFailure(body) };
  if (raw === "completed" && isObject(body.metadata) && body.metadata.result_available === false) {
    return { status: "UNKNOWN", reason: "completed Paipu task reports result unavailable" };
  }
  const result = { status: statuses[raw] };
  if (raw === "failed") result.reason = taskFailure(body);
  const progress = typeof body.progress === "string" ? body.progress.replace(/%$/, "").trim() : body.progress;
  if ((typeof progress === "number" || (typeof progress === "string" && progress !== "")) &&
      Number.isFinite(Number(progress)) && Number(progress) >= 0 && Number(progress) <= 100) result.progress = Number(progress) + "%";
  return result;
}

function videoURLs(body) {
  if (!isObject(body) || (isObject(body.metadata) && body.metadata.result_available === false)) return [];
  const candidates = [body.url, body.result_url, isObject(body.metadata) ? body.metadata.url : undefined];
  return candidates.filter(function (url) { return httpURL(url, false); }).map(function (url) { return url.trim(); });
}

export function buildSubmitRequest(ctx) {
  const body = modelRequest(ctx);
  if (has(FIXED_DURATIONS, body.model)) delete body.duration;
  return {
    url: baseURL(ctx) + "/v1/videos", method: "POST",
    headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json" }),
    body,
  };
}

export function extractUsage(ctx) {
  const body = modelRequest(ctx);
  // Only fields referenced by the saved price expression are charged, so
  // reporting all three lets the admin pick the mode on the price page.
  const name = ctx.upstreamModel || ctx.model;
  const facts = { requests: 1, seconds: body.duration };
  const selected = body.resolution || (has(MODELS, body.model) ? MODELS[body.model][0] : TEMPLATES[name]);
  if (selected) facts.resolution = selected;
  return facts;
}

// Scheduling facts for the host's purchase-cost quote; never billing usage.
// Same validated body and resolution choice as extractUsage; a model without a
// published resolution is untiered ("*").
export function describeSpec(ctx) {
  const body = modelRequest(ctx);
  const name = ctx.upstreamModel || ctx.model;
  const selected = body.resolution || (has(MODELS, body.model) ? MODELS[body.model][0] : TEMPLATES[name]);
  return {
    spec_version: 1,
    output_seconds: body.duration,
    seconds_kind: has(FIXED_DURATIONS, body.model) ? "fixed" : "exact",
    resolution: selected || "*",
    references: {
      video: (body.videos || []).length,
      image: (body.images || []).length,
      audio: (body.audios || []).length,
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
  if (/moderat|sensitive|content policy|policy violation|content violation|violates (the )?(content|usage) polic|prohibit|nsfw|inappropriate|审核|违规|敏感|不合规|invalid (image|video|audio|input|prompt|url)|unsupported (image|video|audio)/.test(text)) return "user";
  return "upstream";
}

export function extractUsageOnComplete() {
  // Preserve frozen submitted facts. Paipu billing/charged_quota is not usage
  // in our host, and its query contract does not promise measured video length.
  return {};
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("upstream create response must be an object");
  const result = taskResult(body);
  if ((body.error || body.success === false) && result.status !== "FAILURE") throw new Error(taskFailure(body));
  const taskId = body.id || body.task_id;
  if (typeof taskId !== "string" || !taskId.trim()) throw new Error(result.reason || "upstream create response has no task id");
  const output = { taskId: taskId.trim(), taskData: body };
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
  const contentPath = baseURL(ctx) + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId) + "/content";
  const method = ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET";
  for (const url of videoURLs(ctx.data)) {
    // A custom channel hostname can still receive Paipu's canonical signed URL.
    const path = url.replace(/^https?:\/\/[^/]+/i, "").split(/[?#]/)[0];
    const signed = /[?&]signature=[^&#]+/.test(url);
    const isContentPath = url === contentPath || url.startsWith(contentPath + "?") ||
      (signed && path === contentPath.replace(/^https?:\/\/[^/]+/i, ""));
    if (isContentPath) {
      const expires = /[?&]expires=(\d+)(?:&|$)/.exec(url);
      // An expired snapshot must not make the host's permanent artifact unusable.
      if (!signed || !expires || Number(expires[1]) <= utils.unixNow() + 30) continue;
    }
    // Signed Paipu URLs and external CDN URLs need no channel credentials.
    // The host enforces SSRF checks on this request and on every redirect.
    return { url, method, credentialless: true };
  }
  // Paipu documents Bearer access without a signature at the fixed content URL.
  return { url: contentPath, method, headers: authHeaders(ctx) };
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      if (!body || !["json", "multipart"].includes(body.kind)) throw new Error("JSON or multipart text fields are required");
      let value = body.value;
      if (body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("file uploads are not supported; send public HTTPS media URLs");
        value = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video request field: " + name);
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
      if (ctx.model && ctx.model !== model) throw new Error("model does not match the selected channel model");
      const requestBody = videoParams(value);
      if (ctx.upstreamModel) modelRequest({ model, upstreamModel: ctx.upstreamModel, requestBody });
      const references = ["images", "videos", "audios"].some(function (name) { return (requestBody[name] || []).length > 0; });
      return { kind: "submit", model, action: references ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      if (!isObject(task.data)) return {};
      const body = task.data;
      const output = {};
      for (const name of ["seconds", "size", "aspect_ratio"]) {
        if (typeof body[name] === "string" || typeof body[name] === "number") output[name] = body[name];
      }
      if (task.status === "SUCCESS") {
        const urls = videoURLs(body);
        if (urls.length) { output.url = urls[0]; output.video_url = urls[0]; output.result_url = urls[0]; }
      }
      if (task.status === "FAILURE") {
        output.error = { message: task.fail_reason || taskFailure(body) };
        if (isObject(body.error)) {
          for (const name of ["code", "phase"]) {
            if (typeof body.error[name] === "string") output.error[name] = body.error[name];
          }
          if (typeof body.error.retryable === "boolean") output.error.retryable = body.error.retryable;
        }
      }
      return output;
    },
  },
};
