// Task Plugin API v1 adapter for the Po Xiao public /v1/videos API.
// The upstream accepts JSON and public media URLs; legacy model names remain supported.
const REQUEST_BILLING_MODELS = ["sd-2-c5", "sd-2-c4","seedance2.0特价1","seedance2.0特价2"];
const MODELS = ["seedance-2.0", "seedance-2.0-fast","seedance-2.0-mini","seedance-2.5", "videos-mini", "videos-fast", "videos-standard", ...REQUEST_BILLING_MODELS];
const RESOLUTIONS = ["480p", "720p", "1080p", "4k"];
const USAGE_SCHEMA = {
  seconds: {
    type: "number",
    unit: "second",
    description: { en: "Video generation unit price", zh: "视频生成单价" },
  },
  resolution: {
    enum: RESOLUTIONS,
    description: { en: "Output video resolution", zh: "输出视频分辨率" },
  },
};
const USAGE_EXAMPLES = RESOLUTIONS.map(function (resolution) {
  return { label: resolution + " · 5s", facts: { seconds: 5, resolution } };
});
const RATIOS = ["16:9", "9:16", "1:1", "21:9", "4:3", "3:4"];
const ALLOWED_FIELDS = [
  "model", "prompt", "seconds", "duration", "size", "video_size", "n",
  "resolution", "video_resolution", "ratio", "aspect_ratio", "aspectRatio",
  "images", "videos", "audios", "referenceImages", "referenceVideos",
  "referenceAudios", "reference_images", "reference_videos",
  "reference_audios", "image", "input_reference", "first_image_url", "last_image_url", "first_image", "last_image",
  "image_urls", "video_urls", "audio_urls", "generate_audio", "metadata",
];
const MEDIA_FIELDS = [
  "images", "videos", "audios", "referenceImages", "referenceVideos",
  "referenceAudios", "reference_images", "reference_videos", "reference_audios",
  "image_urls", "video_urls", "audio_urls",
];
const METADATA_FIELDS = ["content", "seconds", "duration", "resolution", "video_resolution", "ratio", "aspect_ratio", "aspectRatio", "size", "video_size", "generate_audio"];

export const meta = {
  apiVersion: 1,
  key: "seedance-hjmie",
  name: "Seedance via Po Xiao",
  version: "1.0.14",
  author: { name: "jiayi-1994" },
  description: {
    en: "Video generation through the Po Xiao API",
    zh: "通过破晓 API 生成视频",
  },
  icon: "text:SD",
  baseUrl: "https://poxiaoapi001.com",
  models: MODELS,
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: USAGE_SCHEMA,
  usageExamples: USAGE_EXAMPLES,
  usageProfiles: [{
    models: REQUEST_BILLING_MODELS,
    schema: {
      ...USAGE_SCHEMA,
      requests: {
        type: "number",
        unit: "count",
        unitLabel: { en: "request", zh: "次" },
        description: { en: "Video request unit price", zh: "视频请求单价" },
      },
    },
    examples: USAGE_EXAMPLES.map(function (example) {
      return { label: example.label, facts: { ...example.facts, requests: 1 } };
    }),
  }],
};

function has(object, name) {
  return Object.prototype.hasOwnProperty.call(object, name);
}

function resolution(value, field) {
  if (typeof value !== "string" || !value.trim()) throw new Error(field + " must be a resolution string");
  const normalized = value.trim().toLowerCase();
  const tier = normalized === "2160p" ? "4k" : normalized;
  if (!RESOLUTIONS.includes(tier)) {
    throw new Error(field + " has unsupported resolution " + JSON.stringify(value) + "; use " + RESOLUTIONS.join(", "));
  }
  return tier;
}

function ratio(value, field) {
  if (typeof value !== "string" || !RATIOS.includes(value.trim())) {
    throw new Error(field + " must be one of " + RATIOS.join(", "));
  }
  return value.trim();
}

function size(value, field) {
  if (typeof value !== "string") throw new Error(field + " must be a string");
  const text = value.trim().toLowerCase();
  if (!text.includes("x")) return { resolution: resolution(text, field) };
  const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(text);
  if (!match) throw new Error(field + " must be WIDTHxHEIGHT or a supported resolution");
  const width = Number(match[1]);
  const height = Number(match[2]);
  const shortSide = Math.min(width, height);
  const tiers = { 480: "480p", 720: "720p", 1080: "1080p", 2160: "4k" };
  const tier = tiers[shortSide];
  if (!tier) throw new Error(field + " " + value + " does not map to a supported resolution");
  const aspect = width / height;
  const choices = {
    "16:9": 16 / 9, "9:16": 9 / 16, "1:1": 1,
    "21:9": 21 / 9, "4:3": 4 / 3, "3:4": 3 / 4,
  };
  for (const name of RATIOS) {
    if (Math.abs(aspect - choices[name]) < 0.01) return { resolution: tier, ratio: name };
  }
  throw new Error(field + " " + value + " does not map to a supported ratio");
}

function seconds(value, field) {
  if (typeof value !== "string" && typeof value !== "number") throw new Error(field + " must be an integer number of seconds");
  const text = String(value).trim();
  if (!/^[1-9]\d*$/.test(text)) throw new Error(field + " must be an integer number of seconds");
  const parsed = Number(text);
  if (!Number.isSafeInteger(parsed) || parsed > 3600) throw new Error(field + " must be between 1 and 3600");
  return parsed;
}

function boolean(value, field) {
  if (typeof value !== "boolean") throw new Error(field + " must be a boolean");
  return value;
}

function scalarAlias(input, names, parse, metadata = {}) {
  let found;
  for (const source of [input, metadata]) {
    for (const name of names) {
      if (!has(source, name)) continue;
      const value = parse(source[name], name);
      if (found !== undefined && found !== value) throw new Error("conflicting values for " + names.join("/"));
      found = value;
    }
  }
  return found;
}

function frameURLs(input) {
  const frames = {};
  for (const name of ["first_image", "last_image"]) {
    const url = scalarAlias(input, [name + "_url", name], function (value, field) {
      if (typeof value !== "string" || !/^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value.trim())) throw new Error(field + " must be a public http(s) URL");
      return value.trim();
    });
    if (url !== undefined) frames[name + "_url"] = url;
  }
  if (frames.last_image_url && !frames.first_image_url) throw new Error("last_image_url requires first_image_url");
  return frames;
}

function mediaURLs(input, names, label, mirrors = {}) {
  let selected;
  // Equivalent canvas mirrors represent one list. Preserve order and repeated
  // URLs within that list because each submitted reference is independently counted.
  for (const source of [input, mirrors]) {
    for (const name of names) {
      if (!has(source, name)) continue;
      let value = source[name];
      if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && value[0].trim().startsWith("[")) {
        value = value[0];
      }
      if (typeof value === "string") {
        if (value.trim().startsWith("[")) {
          try { value = JSON.parse(value); } catch (_error) { throw new Error(name + " must be a URL array"); }
        } else {
          value = [value];
        }
      }
      if (!Array.isArray(value)) throw new Error(name + " must be a URL array");
      const urls = value.map(function (url) {
        if (typeof url !== "string" || !/^https?:\/\/[^\s/?#@]+(?:[/?#][^\s]*)?$/i.test(url.trim())) {
          throw new Error(name + " entries must be public http(s) URLs");
        }
        return url.trim();
      });
      if (selected !== undefined && JSON.stringify(selected) !== JSON.stringify(urls)) throw new Error("conflicting " + label + " fields: " + names.join("/"));
      selected = urls;
    }
  }
  return selected || [];
}

function decodedFields(body) {
  if (!body || (body.kind !== "json" && body.kind !== "multipart")) throw new Error("JSON or multipart body required");
  if (body.kind === "json") {
    if (!body.value || typeof body.value !== "object" || Array.isArray(body.value)) throw new Error("JSON object required");
    return body.value;
  }
  if ((body.files || []).length) {
    throw new Error("file uploads are not supported by this upstream; upload media to a public URL and send images/videos/audios");
  }
  const input = {};
  for (const rawName of Object.keys(body.fields || {})) {
    const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
    if (has(input, name)) throw new Error("duplicate field " + name);
    const values = body.fields[rawName];
    if (!Array.isArray(values) || !values.length) throw new Error(name + " requires a value");
    if (!MEDIA_FIELDS.includes(name) && values.length !== 1) throw new Error(name + " must be provided once");
    input[name] = MEDIA_FIELDS.includes(name) ? values : values[0];
    if (name === "metadata" || name === "generate_audio") {
      try { input[name] = JSON.parse(values[0]); } catch (_error) { throw new Error(name + " contains invalid JSON"); }
    }
  }
  return input;
}

function normalize(ctx) {
  const input = decodedFields(ctx.body);
  if (has(input, "n") && input.n !== 1 && input.n !== "1") throw new Error("n must be 1");
  for (const name of Object.keys(input)) {
    if (!ALLOWED_FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  const metadata = has(input, "metadata") ? input.metadata : {};
  if (!metadata || typeof metadata !== "object" || Array.isArray(metadata)) throw new Error("metadata must be an object");
  for (const name of Object.keys(metadata)) {
    if (!METADATA_FIELDS.includes(name)) throw new Error("unsupported metadata parameter: " + name);
  }
  const content = has(metadata, "content") ? metadata.content : [];
  if (!Array.isArray(content)) throw new Error("metadata.content must be an array");
  const references = {};
  for (const item of content) {
    if (!item || typeof item !== "object" || Array.isArray(item) || !["image_url", "video_url", "audio_url"].includes(item.type)) {
      throw new Error("unsupported metadata.content type; use image_url, video_url or audio_url");
    }
    const kind = item.type.slice(0, -4);
    if (has(item, "role") && item.role !== "reference_" + kind) throw new Error("unsupported metadata.content role: " + item.role);
    for (const name of Object.keys(item)) {
      if (!["type", "role", item.type].includes(name)) throw new Error("unsupported metadata.content parameter: " + name);
    }
    const media = item[item.type];
    if (!media || typeof media !== "object" || Array.isArray(media) || Object.keys(media).length !== 1 || !has(media, "url")) {
      throw new Error("metadata.content " + item.type + " must contain only url");
    }
    if (typeof media.url !== "string" || !/^https?:\/\/[^\s/?#@]+(?:[/?#][^\s]*)?$/i.test(media.url.trim())) {
      throw new Error("metadata.content entries must be public http(s) URLs");
    }
    const field = kind + "s";
    if (!has(references, field)) references[field] = [];
    references[field].push(media.url);
  }
  const model = input.model;
  if (typeof model !== "string" || !model.trim()) throw new Error("model is required");
  if (ctx.model && model !== ctx.model) throw new Error("model does not match the selected model");
  // The first decode precedes channel selection, so a public alias may not yet
  // have an upstream identity. Submission and describeSpec validate it again.
  if (ctx.upstreamModel) upstreamModel(ctx);
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");

  const duration = scalarAlias(input, ["seconds", "duration"], seconds, metadata);
  if (duration === undefined) throw new Error("seconds or duration is required");

  let selected = scalarAlias(input, ["resolution", "video_resolution"], resolution, metadata);
  let selectedRatio = scalarAlias(input, ["ratio", "aspect_ratio", "aspectRatio"], ratio, metadata);
  for (const source of [input, metadata]) {
    for (const name of ["size", "video_size"]) {
      if (!has(source, name)) continue;
      const mapped = size(source[name], name);
      if (selected !== undefined && selected !== mapped.resolution) throw new Error("size conflicts with resolution");
      if (mapped.ratio && selectedRatio !== undefined && selectedRatio !== mapped.ratio) throw new Error("size conflicts with ratio");
      selected = mapped.resolution;
      if (mapped.ratio) selectedRatio = mapped.ratio;
    }
  }
  if (selected === undefined) throw new Error("resolution or size is required; refusing an implicit 720p default");

  const images = mediaURLs(input, ["images", "image_urls", "referenceImages", "reference_images", "image", "input_reference"], "image reference", references);
  const videos = mediaURLs(input, ["videos", "video_urls", "referenceVideos", "reference_videos"], "video reference", references);
  const audios = mediaURLs(input, ["audios", "audio_urls", "referenceAudios", "reference_audios"], "audio reference", references);
  const requestBody = { prompt: input.prompt, duration, resolution: selected };
  // Preserve the canvas switch without a default; this is independent of
  // reference audio and does not establish the upstream model's audio support.
  const generateAudio = scalarAlias(input, ["generate_audio"], boolean, metadata);
  if (generateAudio !== undefined) requestBody.generate_audio = generateAudio;
  if (selectedRatio) requestBody.ratio = selectedRatio;
  if (images.length) requestBody.images = images;
  if (videos.length) requestBody.videos = videos;
  if (audios.length) requestBody.audios = audios;
  const frames = frameURLs(input);
  Object.assign(requestBody, frames);
  return {
    kind: "submit", model,
    action: images.length || videos.length || audios.length || frames.first_image_url ? "reference_to_video" : "text_to_video",
    requestBody,
  };
}

function upstreamModel(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (!MODELS.includes(model)) throw new Error("unsupported upstream video model: " + model);
  return model;
}

function authHeaders(ctx) {
  // The host passes a raw key as authHeader on vendor channels and a complete
  // Bearer header on New API channels; normalize both to one Bearer header.
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string") throw new Error("channel API key is required");
  const token = credential.trim().replace(/^Bearer\s+/i, "").trim();
  if (!token || /\s/.test(token)) throw new Error("channel API key is required and must not contain whitespace");
  return { Authorization: "Bearer " + token };
}

export function buildSubmitRequest(ctx) {
  const input = ctx.requestBody || {};
  const model = upstreamModel(ctx);
  const body = {
    model,
    prompt: input.prompt,
    duration: seconds(input.duration, "duration"),
    resolution: resolution(input.resolution, "resolution"),
  };
  if (input.ratio !== undefined) body.ratio = ratio(input.ratio, "ratio");
  if (has(input, "generate_audio")) body.generate_audio = boolean(input.generate_audio, "generate_audio");
  for (const name of ["images", "videos", "audios"]) {
    if (input[name] !== undefined) body[name] = mediaURLs(input, [name], name);
  }
  Object.assign(body, frameURLs(input));
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos",
    method: "POST",
    headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json" }),
    body,
  };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!body || typeof body !== "object" || Array.isArray(body)) throw new Error("upstream create response must be an object");
  const result = parseTaskResult(_ctx, body);
  const ids = [body.task_id, body.id];
  if (ids.some(value => value != null && typeof value !== "string")) throw new Error("upstream create response has an invalid task id");
  const taskId = ids.find(value => typeof value === "string" && value.trim());
  if ((body.error || body.success === false) && result.status !== "FAILURE") {
    const acceptedStatus = [body.status].some(value => value != null && String(value).trim() !== "" && !/^(failed|failure|cancelled|canceled)(?:[:：].*)?$/i.test(String(value).trim()));
    if (taskId || acceptedStatus) throw new Error("upstream create response has conflicting acceptance and rejection signals");
    const reason = body.error && typeof body.error === "object" ? body.error.message || body.message : body.error || body.message;
    return { rejected: { reason: typeof reason === "string" && reason ? reason : "upstream rejected video creation" } };
  }
  if (typeof taskId !== "string" || !taskId.trim()) {
    if (result.status === "FAILURE") return { rejected: { reason: result.reason } };
    throw new Error("upstream create response has no task id");
  }
  const output = { taskId: taskId.trim(), taskData: body };
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function extractUsage(ctx) {
  const input = ctx.requestBody || {};
  const facts = {
    seconds: seconds(input.duration, "duration"),
    resolution: resolution(input.resolution, "resolution"),
  };
  // Match the host's upstream-first profile selection and client-model fallback.
  // A submission creates one video; completion retains this reserved count.
  if (REQUEST_BILLING_MODELS.includes(ctx.upstreamModel) || REQUEST_BILLING_MODELS.includes(ctx.model)) {
    facts.requests = 1;
    // Legacy per-call prices multiply all numeric facts. Keep duration only
    // for expressions so a fixed request price is not multiplied by seconds.
    if (ctx.usagePurpose === "billing_ratios") delete facts.seconds;
  }
  return facts;
}

function reportedResolution(data) {
  if (data.resolution !== undefined) return resolution(data.resolution, "resolution");
  if (data.size !== undefined) return size(data.size, "size").resolution;
  const metadata = data.metadata;
  if (metadata && typeof metadata === "object" && !Array.isArray(metadata) && metadata.resolution !== undefined) {
    return resolution(metadata.resolution, "metadata.resolution");
  }
}

export function extractUsageOnComplete(_task, _result, data) {
  if (!data || typeof data !== "object" || Array.isArray(data)) return {};
  const facts = {};
  const duration = data.duration === undefined ? data.seconds : data.duration;
  if (duration !== undefined) {
    try { facts.seconds = seconds(duration, "duration"); } catch (_error) { /* retain reserved seconds */ }
  }
  try {
    const selected = reportedResolution(data);
    if (selected !== undefined) facts.resolution = selected;
  } catch (_error) { /* retain reserved resolution */ }
  return facts;
}

export function buildQueryRequest(ctx) {
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: authHeaders(ctx),
  };
}

export function parseTaskResult(_ctx, body) {
  if (!body || typeof body !== "object" || Array.isArray(body)) return { status: "UNKNOWN", reason: "invalid task response" };
  const raw = typeof body.status === "string" ? body.status.trim() : "";
  const status = raw.toLowerCase();
  const statuses = {
    queued: "QUEUED", pending: "QUEUED", submitted: "QUEUED", not_start: "QUEUED",
    processing: "IN_PROGRESS", in_progress: "IN_PROGRESS", running: "IN_PROGRESS",
    completed: "SUCCESS", succeeded: "SUCCESS", success: "SUCCESS",
    failed: "FAILURE", failure: "FAILURE", cancelled: "FAILURE", canceled: "FAILURE",
  };
  const mapped = status.startsWith("failed:") ? "FAILURE" : statuses[status];
  if (!mapped) return { status: "UNKNOWN", reason: "unrecognized status: " + raw };
  const result = { status: mapped };
  const progress = Number(body.progress);
  if (body.progress !== undefined && Number.isFinite(progress) && progress >= 0 && progress <= 100) {
    result.progress = progress + "%";
  }
  if (mapped === "FAILURE") {
    const error = body.error;
    const code = error && typeof error === "object" ? error.code : undefined;
    const message = error && typeof error.message === "string" ? error.message.trim() : "";
    const detail = message && message.toLowerCase() !== "video generation failed" ? message : "";
    // Gateways may opt in to sanitized details. Keep both those details and
    // their explicit public attribution when forwarding through another gateway.
    if (code === "video_request_rejected") {
      result.reason = "video request rejected" + (detail ? ": " + detail : "");
      return result;
    }
    if (code === "video_generation_cancelled") {
      result.reason = "cancelled: " + (detail || "video generation cancelled");
      return result;
    }
    const text = (error && typeof error === "object" && error.message) ||
      (typeof error === "string" && error) || body.fail_reason ||
      (status.startsWith("failed:") && raw.slice(raw.indexOf(":") + 1).trim()) || "";
    // classifyFailure only sees the reason, so a cancelled status must say so.
    const cancelled = status === "cancelled" || status === "canceled";
    result.reason = cancelled ? "cancelled: " + (text || "video generation cancelled") : text || "video generation failed";
  }
  return result;
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  if (!ctx.upstreamTaskId) throw new Error("upstream task id is missing");
  const data = ctx.data && typeof ctx.data === "object" && !Array.isArray(ctx.data) ? ctx.data : {};
  const metadata = data.metadata && typeof data.metadata === "object" && !Array.isArray(data.metadata) ? data.metadata : {};
  // Use a public result URL without credentials when one is supplied. Otherwise
  // the canonical /content endpoint needs channel authentication. A compatible
  // host relays recognized object-storage redirects without sending credentials
  // to storage; other credentialed cross-origin redirects remain rejected.
  for (const candidate of [metadata.final_video_url, data.video_url, data.url]) {
    if (typeof candidate === "string" && /^https?:\/\/[^\s/?#@]+(?:[/?#][^\s]*)?$/i.test(candidate.trim())) {
      return { url: candidate.trim(), method: ctx.clientRequest.method, credentialless: true };
    }
  }
  // Unified responses contain metadata.url with a relative gateway content path.
  // Build that path from the persisted upstream ID rather than trusting metadata.
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId) + "/content",
    method: ctx.clientRequest.method,
    headers: authHeaders(ctx),
  };
}

// Scheduling facts for the host's purchase-cost quote; never billing usage.
// Reads the decoded body the same way extractUsage and buildSubmitRequest do.
export function describeSpec(ctx) {
  upstreamModel(ctx);
  const input = ctx.requestBody || {};
  const frames = frameURLs(input);
  return {
    spec_version: 3,
    reference_video_urls: mediaURLs(input, ["videos"], "videos"),
    output_seconds: seconds(input.duration, "duration"),
    seconds_kind: "exact",
    resolution: resolution(input.resolution, "resolution"),
    references: {
      video: mediaURLs(input, ["videos"], "videos").length,
      image: mediaURLs(input, ["images"], "images").length,
      audio: mediaURLs(input, ["audios"], "audios").length,
      frame: Object.keys(frames).length,
    },
  };
}

// classifyFailure attributes a terminal failure reason for channel health:
// content or input rejections are the user's, cancellations are neutral, and
// anything else counts against the upstream channel.
export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (text === "video request rejected" || text.startsWith("video request rejected: ")) return "user";
  // Only the marker this plugin writes for a cancelled status, or an explicit
  // user cancellation, is neutral; a provider-side cancellation is upstream.
  if (/^cancelled: |cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  // Mentioning moderation or validation infrastructure is not a user rejection.
  // Keep service failures upstream even if their message contains these words.
  if (/timeout|timed out|service (is temporarily )?unavailable|internal (server )?error|quota (exhausted|exceeded)|insufficient (quota|balance)|rate limit|connection|authentication|permission denied|超时|服务(暂)?不可用|内部错误|额度(不足|耗尽)|限流/.test(text)) return "upstream";
  if (/content policy violation|content violation|violates (the )?(content|usage) polic|moderation (rejected|blocked)|rejected by (the )?(content )?moderation|审核(未通过|不通过|拒绝)|未通过(内容)?审核|内容.{0,12}(违规|敏感|不合规)|(提示词|素材|图片|视频|输入文本).{0,12}(敏感|违规)|触发敏感词|invalid (image|video|audio|prompt|url)\b|unsupported (image|video|audio) (format|type)\b/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: normalize,
    render: function (_ctx, task) {
      const data = task.data;
      if (!data || typeof data !== "object" || Array.isArray(data)) return {};
      const output = {};
      for (const name of ["seconds", "duration", "size", "resolution", "ratio", "error", "fail_reason"]) {
        if (has(data, name)) output[name] = data[name];
      }
      if (!has(output, "resolution")) {
        try {
          const selected = reportedResolution(data);
          if (selected !== undefined) output.resolution = selected;
        } catch (_error) { /* omit unrecognized upstream resolution */ }
      }
      if (task.status === "SUCCESS") {
        if (typeof data.url === "string") output.url = data.url;
        if (typeof data.video_url === "string") output.video_url = data.video_url;
      }
      return output;
    },
  },
};
