// Task Plugin API v1 for https://newapi.megabyai.cc/video-docs.
// Self-contained: install this file in an official New API instance.
const MODELS = ["videos-mini", "videos-fast", "videos-standard", "seedance-2.0", "sd-2-720p"];
// 分辨率开关：仅保留要开放的档位，至少保留一个；删去后重新上传插件并保存价格。
// 官方插件无法读取后台价格，价格留空/填 0 不会禁用；此列表同步控制校验和展示。
const RESOLUTIONS = ["480p", "720p", "1080p", "4k"];
const RATIOS = ["16:9", "9:16", "1:1", "21:9", "4:3", "3:4"];
const MAX_SECONDS = 3600; // Official host ceiling for usage fields with unit "second".
const MAX_REFERENCES = 128;
const MEDIA_FIELDS = [
  "referenceImages", "referenceVideos", "referenceAudios",
  "reference_images", "reference_videos", "reference_audios",
  "images", "videos", "audios", "image", "input_reference",
];
const FIELDS = [
  "model", "prompt", "seconds", "duration", "size", "video_size", "n",
  "resolution", "video_resolution", "ratio", "aspect_ratio", "aspectRatio",
].concat(MEDIA_FIELDS);
// Per reference kind: the aliases videoParams reads (the first is Mega's field), and the
// copies multi-gateway clients add: a `*_urls` mirror and a metadata.content entry type.
const REFERENCE_KINDS = [
  { name: "image", aliases: ["referenceImages", "reference_images", "images", "image", "input_reference"], mirror: "image_urls", content: "image_url" },
  { name: "video", aliases: ["referenceVideos", "reference_videos", "videos"], mirror: "video_urls", content: "video_url" },
  { name: "audio", aliases: ["referenceAudios", "reference_audios", "audios"], mirror: "audio_urls", content: "audio_url" },
];
const METADATA_SCALARS = ["duration", "seconds", "resolution", "ratio", "aspect_ratio"];

export const meta = {
  apiVersion: 1,
  key: "megabyai",
  name: "Mega Video",
  version: "2.1.7",
  author: { name: "jiayi-1994" },
  description: { en: "Video generation through the Mega API", zh: "通过 Mega API 生成视频" },
  icon: "text:M",
  baseUrl: "https://newapi.megabyai.cc",
  models: MODELS,
  fetchMode: "per_task",
  upstreams: ["vendor", "new_api"],
  protocols: ["openai_video"],
  usageSchema: {
    seconds: {
      type: "number", unit: "second",
      description: { en: "Video generation unit price", zh: "视频生成单价" },
    },
    surcharge_seconds: {
      type: "number", unit: "second",
      description: { en: "Video generation surcharge unit price", zh: "视频生成加收单价" },
    },
    resolution: {
      enum: RESOLUTIONS,
      description: { en: "Output video resolution", zh: "输出视频分辨率" },
    },
  },
  usageExamples: RESOLUTIONS.map(function (value) {
    return { label: value + " · 5s", facts: { seconds: 5, surcharge_seconds: 5, resolution: value } };
  }),
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function duration(value, field) {
  if (typeof value !== "number" && typeof value !== "string") throw new Error(field + " must be an integer number of seconds");
  const text = String(value).trim();
  if (!/^[1-9]\d*$/.test(text)) throw new Error(field + " must be an integer number of seconds");
  const number = Number(text);
  if (!Number.isSafeInteger(number) || number > MAX_SECONDS) throw new Error(field + " must be between 1 and " + MAX_SECONDS);
  return number;
}

function resolution(value, field) {
  if (typeof value !== "string" || !value.trim()) throw new Error(field + " must be a resolution string");
  let result = value.trim().toLowerCase();
  if (result === "2160p") result = "4k";
  if (!RESOLUTIONS.includes(result)) throw new Error(field + " must be one of " + RESOLUTIONS.join(", "));
  return result;
}

function ratio(value, field) {
  if (typeof value !== "string" || !RATIOS.includes(value.trim())) throw new Error(field + " must be one of " + RATIOS.join(", "));
  return value.trim();
}

function videoSize(value, field) {
  if (typeof value !== "string") throw new Error(field + " must be WIDTHxHEIGHT or a resolution");
  const text = value.trim().toLowerCase();
  if (!text.includes("x")) return { resolution: resolution(text, field) };
  const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(text);
  if (!match) throw new Error(field + " must be WIDTHxHEIGHT or a resolution");
  const width = Number(match[1]);
  const height = Number(match[2]);
  const tier = { 480: "480p", 720: "720p", 1080: "1080p", 2160: "4k" }[Math.min(width, height)];
  if (!tier || !RESOLUTIONS.includes(tier)) throw new Error(field + " does not map to a supported resolution; send resolution and ratio explicitly");
  const aspects = { "16:9": 16 / 9, "9:16": 9 / 16, "1:1": 1, "21:9": 21 / 9, "4:3": 4 / 3, "3:4": 3 / 4 };
  for (const name of RATIOS) {
    if (Math.abs(width / height - aspects[name]) < 0.01) return { resolution: tier, ratio: name };
  }
  throw new Error(field + " does not map to a supported ratio; send resolution and ratio explicitly");
}

function scalarAlias(input, names, parse) {
  let result;
  for (const name of names) {
    if (!has(input, name)) continue;
    const value = parse(input[name], name);
    if (result !== undefined && result !== value) throw new Error("conflicting values for " + names.join("/"));
    result = value;
  }
  return result;
}

function httpURL(value) {
  return typeof value === "string" && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value.trim());
}

function mediaURLs(input, names) {
  let result;
  for (const name of names) {
    if (!has(input, name)) continue;
    if (result !== undefined) throw new Error("provide only one reference field: " + names.join("/"));
    let value = input[name];
    // Multipart fields contain arrays of strings, including a serialized JSON array.
    if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && /^[\[{]/.test(value[0].trim())) value = value[0];
    if (typeof value === "string" && /^\[https?:\/\//i.test(value.trim())) throw new Error(name + " must contain raw public http(s) URLs, not Markdown links");
    if (typeof value === "string" && /^[\[{]/.test(value.trim())) {
      try { value = JSON.parse(value); } catch (_error) { throw new Error(name + " contains invalid JSON"); }
    }
    if (name === "input_reference" && isObject(value)) {
      if (has(value, "file_id")) throw new Error("input_reference.file_id is not supported; send a public image_url");
      if (Object.keys(value).length !== 1 || !has(value, "image_url")) throw new Error("input_reference must contain only image_url");
      value = value.image_url;
    }
    if (typeof value === "string") value = [value];
    if (!Array.isArray(value)) throw new Error(name + " must be a public URL or URL array");
    if (value.length > MAX_REFERENCES) throw new Error("at most " + MAX_REFERENCES + " reference URLs are allowed");
    result = value.map(function (url) {
      if (!httpURL(url)) throw new Error(name + " entries must be public http(s) URLs without embedded credentials");
      return url.trim();
    });
  }
  return result || [];
}

function bodyFields(body) {
  if (!body || (body.kind !== "json" && body.kind !== "multipart")) throw new Error("JSON or multipart text fields are required");
  if (body.kind === "json") {
    if (!isObject(body.value)) throw new Error("JSON body must be an object");
    return body.value;
  }
  if ((body.files || []).length) throw new Error("Mega accepts public reference URLs; upload files to storage first, then send referenceImages or input_reference.image_url");
  const input = {};
  const listFields = MEDIA_FIELDS.concat(REFERENCE_KINDS.map(function (kind) { return kind.mirror; }));
  for (const rawName of Object.keys(body.fields || {})) {
    const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
    if (has(input, name)) throw new Error("duplicate field: " + name);
    const values = body.fields[rawName];
    if (!Array.isArray(values) || !values.length) throw new Error(name + " requires a value");
    if (!listFields.includes(name) && values.length !== 1) throw new Error(name + " must be provided once");
    input[name] = listFields.includes(name) ? values : values[0];
  }
  return input;
}

// Multi-gateway clients such as the infinite canvas send one request shape everywhere:
// every reference list again under `*_urls` and as volcengine-style metadata.content
// entries, the knobs repeated inside metadata, plus generate_audio. Fold those copies into
// the fields videoParams reads and fail when they disagree. Other unknown fields stay
// rejected, so a misspelt reference field never silently drops a paid reference.
// Decode-only: usage, spec and submission still validate the canonical body strictly, and
// metadata scalars go through the same duration/resolution/ratio parsers as the top level.
function canonicalClientInput(input) {
  if (!isObject(input)) return input;
  const output = Object.assign({}, input);
  if (has(output, "generate_audio")) {
    // Mega's create body has no audio switch, so the flag is checked and not forwarded.
    if (![true, false, "true", "false"].includes(output.generate_audio)) throw new Error("generate_audio must be true or false");
    delete output.generate_audio;
  }
  let metadata = {};
  if (has(output, "metadata")) {
    metadata = output.metadata;
    delete output.metadata;
    if (typeof metadata === "string") {
      try { metadata = JSON.parse(metadata); } catch (_error) { throw new Error("metadata contains invalid JSON"); }
    }
    if (!isObject(metadata)) throw new Error("metadata must be an object");
  }
  for (const name of Object.keys(metadata)) {
    if (name === "content") continue;
    if (!METADATA_SCALARS.includes(name)) throw new Error("unsupported metadata parameter: " + name);
    if (!has(output, name)) output[name] = metadata[name];
    else if (String(output[name]) !== String(metadata[name])) throw new Error("metadata." + name + " conflicts with " + name);
  }
  const content = has(metadata, "content") ? metadata.content : [];
  if (!Array.isArray(content)) throw new Error("metadata.content must be an array");
  for (const item of content) {
    if (!isObject(item) || !REFERENCE_KINDS.some(function (kind) { return item.type === kind.content; })) throw new Error("metadata.content supports only image_url, video_url and audio_url entries");
  }
  for (const kind of REFERENCE_KINDS) {
    const copies = [];
    if (has(output, kind.mirror)) {
      copies.push(mediaURLs(output, [kind.mirror]));
      delete output[kind.mirror];
    }
    const entries = content.filter(function (item) { return item.type === kind.content; });
    if (entries.length) {
      copies.push(entries.map(function (item) {
        // first_frame/last_frame condition output frames; only references map onto Mega's lists.
        if (has(item, "role") && item.role !== "reference_" + kind.name) throw new Error("metadata.content role " + item.role + " is not supported; only reference media can be sent");
        if (!isObject(item[kind.content]) || !httpURL(item[kind.content].url)) throw new Error("metadata.content " + kind.content + " entries must carry a public http(s) url");
        return item[kind.content].url.trim();
      }));
    }
    if (!copies.length) continue;
    const primary = mediaURLs(output, kind.aliases);
    const urls = primary.length ? primary : copies[0];
    for (const copy of copies) {
      if (copy.join("\n") !== urls.join("\n")) throw new Error("mirrored " + kind.name + " references disagree; send one list of reference URLs");
    }
    if (!primary.length) output[kind.aliases[0]] = urls;
  }
  return output;
}

// Decode, forwarding and usage extraction share the exact same validated values.
function videoParams(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  if (has(input, "n") && input.n !== 1 && input.n !== "1") throw new Error("n must be 1");
  for (const name of Object.keys(input)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  const seconds = scalarAlias(input, ["duration", "seconds"], duration);
  if (seconds === undefined) throw new Error("duration or seconds is required for per-second billing");
  let selectedResolution = scalarAlias(input, ["resolution", "video_resolution"], resolution);
  let selectedRatio = scalarAlias(input, ["ratio", "aspect_ratio", "aspectRatio"], ratio);
  for (const name of ["size", "video_size"]) {
    if (!has(input, name)) continue;
    const size = videoSize(input[name], name);
    if (selectedResolution !== undefined && selectedResolution !== size.resolution) throw new Error("size conflicts with resolution");
    if (size.ratio && selectedRatio !== undefined && selectedRatio !== size.ratio) throw new Error("size conflicts with ratio");
    selectedResolution = size.resolution;
    if (size.ratio) selectedRatio = size.ratio;
  }
  if (selectedResolution === undefined) throw new Error("resolution or size is required; no implicit 720p default");
  const [images, videos, audios] = REFERENCE_KINDS.map(function (kind) { return mediaURLs(input, kind.aliases); });
  if (images.length + videos.length + audios.length > MAX_REFERENCES) throw new Error("at most " + MAX_REFERENCES + " reference URLs are allowed");
  const output = { prompt: input.prompt, duration: seconds, resolution: selectedResolution };
  if (selectedRatio !== undefined) output.ratio = selectedRatio;
  if (images.length) output.referenceImages = images;
  if (videos.length) output.referenceVideos = videos;
  if (audios.length) output.referenceAudios = audios;
  return output;
}

function baseURL(ctx) {
  if (typeof ctx.baseUrl !== "string" || !/^https?:\/\/[^\s/?#@\\]+(?:\/[^\s?#\\]*)?$/i.test(ctx.baseUrl)) throw new Error("channel Base URL must be an http(s) address without query, fragment or credentials");
  return ctx.baseUrl.replace(/\/+$/, "");
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

function taskRecord(body) {
  if (!isObject(body)) throw new Error("upstream task response must be a JSON object");
  if (isObject(body.data) && (typeof body.data.status === "string" || (!body.status && (body.data.id || body.data.task_id)))) return body.data;
  return body;
}

function taskFailure(record, body) {
  for (const value of [record.error, body.error]) {
    if (isObject(value) && typeof value.message === "string" && value.message) return value.message;
    if (typeof value === "string" && value) return value;
  }
  for (const value of [record.fail_reason, body.fail_reason, record.message, body.message]) {
    if (typeof value === "string" && value) return value;
  }
  return "video generation failed";
}

function taskResult(body) {
  const record = taskRecord(body);
  const raw = typeof record.status === "string" ? record.status.trim() : "";
  const status = raw.toLowerCase();
  const statuses = {
    queued: "QUEUED", pending: "QUEUED", submitted: "QUEUED", not_start: "QUEUED",
    in_progress: "IN_PROGRESS", processing: "IN_PROGRESS", running: "IN_PROGRESS",
    completed: "SUCCESS", succeeded: "SUCCESS",
    failed: "FAILURE", failure: "FAILURE", cancelled: "FAILURE", canceled: "FAILURE",
  };
  const mapped = status.startsWith("failed:") ? "FAILURE" : statuses[status];
  if (!mapped) return { status: "UNKNOWN", reason: "unrecognized video task status: " + raw };
  const result = { status: mapped };
  const progress = typeof record.progress === "string" ? record.progress.replace(/%$/, "").trim() : record.progress;
  if ((typeof progress === "number" || (typeof progress === "string" && progress !== "")) && Number.isFinite(Number(progress)) && Number(progress) >= 0 && Number(progress) <= 100) result.progress = Number(progress) + "%";
  if (mapped === "FAILURE") result.reason = status.startsWith("failed:") ? raw.slice(raw.indexOf(":") + 1).trim() || taskFailure(record, body) : taskFailure(record, body);
  // classifyFailure only sees the reason, so a cancelled status must say so.
  if (status === "cancelled" || status === "canceled") {
    result.reason = "cancelled: " + (result.reason === "video generation failed" ? "video generation cancelled" : result.reason);
  }
  return result;
}

function videoURLs(body) {
  if (!isObject(body)) return [];
  const record = taskRecord(body);
  const scopes = [record, record.data, body];
  const urls = [];
  for (const scope of scopes) {
    if (!isObject(scope)) continue;
    const candidates = [scope.video_url, scope.url];
    for (const list of [scope.results, Array.isArray(scope.data) ? scope.data : []]) {
      if (!Array.isArray(list)) continue;
      for (const item of list) {
        if (isObject(item)) candidates.push(item.video_url, item.url);
      }
    }
    for (const candidate of candidates) {
      if (httpURL(candidate) && !urls.includes(candidate.trim())) urls.push(candidate.trim());
    }
  }
  return urls;
}

export function buildSubmitRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (typeof model !== "string" || !model.trim()) throw new Error("upstream model is required");
  const body = Object.assign({ model: model.trim() }, videoParams(ctx.requestBody));
  return {
    url: baseURL(ctx) + "/v1/videos",
    method: "POST",
    headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json" }),
    body,
  };
}

export function extractUsage(ctx) {
  const params = videoParams(ctx.requestBody);
  // Both prices apply to the same validated output duration, never client-supplied usage.
  return { seconds: params.duration, surcharge_seconds: params.duration, resolution: params.resolution };
}

// Scheduling facts for the host's purchase-cost quote; never billing usage.
// surcharge_seconds is part of the per-second base price, not a reference fact.
export function describeSpec(ctx) {
  const params = videoParams(ctx.requestBody);
  return {
    spec_version: 2,
    reference_video_urls: params.referenceVideos || [],
    output_seconds: params.duration,
    seconds_kind: "exact",
    resolution: params.resolution,
    references: {
      video: (params.referenceVideos || []).length,
      image: (params.referenceImages || []).length,
      audio: (params.referenceAudios || []).length,
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
  if (/出于肖像保护考虑，未认证人脸暂不支持/.test(text)) return "user";
  return "upstream";
}

// Mega's public query contract does not specify measured duration/usage.
// Keep the validated submitted facts frozen; do not invent usage from a missing field.
export function extractUsageOnComplete() {
  return {};
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  const record = taskRecord(body);
  const result = taskResult(body);
  const ids = [record.task_id, record.id, body.task_id, body.id];
  if (ids.some(value => value != null && typeof value !== "string")) throw new Error("upstream create response has an invalid task id");
  const taskId = ids.find(value => typeof value === "string" && value.trim());
  if ((record.error || body.error || body.success === false) && result.status !== "FAILURE") {
    const acceptedStatus = [record.status, body.status].some(value => value != null && String(value).trim() !== "" && !/^(failed|failure|cancelled|canceled)(?:[:：].*)?$/i.test(String(value).trim()));
    if (taskId || acceptedStatus) throw new Error("upstream create response has conflicting acceptance and rejection signals");
    return { rejected: { reason: taskFailure(record, body) } };
  }
  if (typeof taskId !== "string" || !taskId.trim()) {
    if (result.status === "FAILURE") return { rejected: { reason: taskFailure(record, body) } };
    throw new Error("upstream create response has no task id");
  }
  const output = { taskId: taskId.trim(), taskData: body };
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId) throw new Error("upstream task id is required");
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
  const urls = videoURLs(ctx.data);
  if (!urls.length) throw new Error("completed Mega task has no downloadable video URL");
  const base = baseURL(ctx);
  const origin = /^https?:\/\/[^/]+/i.exec(base)[0].toLowerCase();
  // Prefer a direct storage URL over an authenticated gateway URL that may redirect.
  const external = urls.find(function (url) { return /^https?:\/\/[^/]+/i.exec(url)[0].toLowerCase() !== origin; });
  const url = external || urls[0];
  const method = ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET";
  const contentPath = base + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId || "") + "/content";
  if (!external && ctx.upstreamTaskId && (url === contentPath || url === contentPath + ".mp4" || url.startsWith(contentPath + "?") || url.startsWith(contentPath + ".mp4?"))) {
    return { url, method, headers: authHeaders(ctx) };
  }
  // The official host validates credentialless download URLs with its SSRF guard.
  return { url, method, credentialless: true };
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const input = bodyFields(ctx.body);
      if (typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      const model = input.model.trim();
      if (ctx.model && model !== ctx.model) throw new Error("model does not match the selected channel model");
      const requestBody = videoParams(canonicalClientInput(input));
      const hasReferences = requestBody.referenceImages || requestBody.referenceVideos || requestBody.referenceAudios;
      return { kind: "submit", model, action: hasReferences ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      if (!isObject(task.data)) return {};
      const record = taskRecord(task.data);
      const output = {};
      for (const key of ["seconds", "duration", "size", "resolution", "ratio"]) {
        if (typeof record[key] === "string" || typeof record[key] === "number") output[key] = record[key];
      }
      if (task.status === "SUCCESS") {
        const urls = videoURLs(task.data);
        if (urls.length) { output.video_url = urls[0]; output.url = urls[0]; }
      }
      if (task.status === "FAILURE") output.error = { message: taskResult(task.data).reason || taskFailure(record, task.data) };
      return output;
    },
  },
};
