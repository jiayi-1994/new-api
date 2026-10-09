// Task Plugin API v1 for https://open.bytefor.com/open-api, driven through its
// Volcengine Ark compatible task API (/api/v3/contents/generations/tasks).
// Self-contained: install this file in an official New API instance.

// Capabilities from the open-api page, not prices. bytefor-2.5 documents 480p/720p,
// 4-30 seconds and up to 30 images, 10 videos and 10 audios; the other models document
// 4-15 seconds. Model names reached through channel mapping use the loosest profile and
// are validated again with the mapped upstream name before reservation and submission.
const ALL_RESOLUTIONS = ["480p", "720p", "1080p", "4k"];
const MODELS = {
  "bytefor-2.5": { resolutions: ["480p", "720p"], maxSeconds: 30 },
  "bytefor-2.0-real-priority": { resolutions: ALL_RESOLUTIONS, maxSeconds: 15 },
  "bytefor-2.0-real-standard": { resolutions: ALL_RESOLUTIONS, maxSeconds: 15 },
  "bytefor-2.0-fast": { resolutions: ALL_RESOLUTIONS, maxSeconds: 15 },
};
const DEFAULT_PROFILE = { resolutions: ALL_RESOLUTIONS, maxSeconds: 30 };
const MIN_SECONDS = 4;
const DEFAULT_SECONDS = 5;
const DEFAULT_RESOLUTION = "720p";
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive"];
// Ark content entry type, accepted roles and the documented per-request maximum.
const REFERENCE_KINDS = [
  { name: "image", type: "image_url", list: "images", roles: ["first_frame", "last_frame", "reference_image"], max: 30 },
  { name: "video", type: "video_url", list: "videos", roles: ["reference_video"], max: 10 },
  { name: "audio", type: "audio_url", list: "audios", roles: ["reference_audio"], max: 10 },
];
const FIELDS = ["model", "prompt", "content", "metadata", "duration", "seconds", "resolution", "size", "ratio", "aspect_ratio", "generate_audio", "return_last_frame", "n", "images", "videos", "audios", "first_image_url", "last_image_url", "first_image", "last_image"];
const METADATA_FIELDS = ["content", "duration", "seconds", "resolution", "ratio", "aspect_ratio", "generate_audio", "return_last_frame"];

const RESOLUTION_FIELD = { description: { en: "Output video resolution", zh: "输出视频分辨率" } };
const USAGE_SCHEMA = {
  seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
  resolution: Object.assign({ enum: ALL_RESOLUTIONS }, RESOLUTION_FIELD),
  video_input: { type: "boolean", description: { en: "Reference video present", zh: "存在参考视频" } },
};

function usageExamples(resolutions) {
  return resolutions.map(function (resolution) {
    return { label: resolution + " · 5s", facts: { seconds: 5, resolution, video_input: false } };
  });
}

export const meta = {
  apiVersion: 1,
  key: "bytefor",
  name: "Bytefor",
  version: "1.0.1",
  author: { name: "jiayi-1994" },
  description: { en: "Video generation through the Bytefor Ark compatible API", zh: "通过 Bytefor 火山方舟兼容接口生成视频" },
  icon: "text:B",
  baseUrl: "https://open.bytefor.com",
  models: Object.keys(MODELS),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: USAGE_SCHEMA,
  usageExamples: usageExamples(ALL_RESOLUTIONS),
  usageProfiles: [{
    models: ["bytefor-2.5"],
    schema: Object.assign({}, USAGE_SCHEMA, { resolution: Object.assign({ enum: MODELS["bytefor-2.5"].resolutions }, RESOLUTION_FIELD) }),
    examples: usageExamples(MODELS["bytefor-2.5"].resolutions),
  }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value) {
  return typeof value === "string" && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value.trim());
}

function videoProfile(ctx) {
  for (const name of [ctx.upstreamModel, ctx.model]) {
    if (typeof name === "string" && has(MODELS, name.trim())) return MODELS[name.trim()];
  }
  return DEFAULT_PROFILE;
}

// The same value may arrive under several aliases (and again inside metadata);
// every copy must agree so the billed value is the value sent upstream.
function aliased(sources, names, parse) {
  let result;
  for (const source of sources) {
    for (const name of names) {
      if (!has(source, name)) continue;
      const value = parse(source[name], name);
      if (result !== undefined && result !== value) throw new Error("conflicting values for " + names.join("/"));
      result = value;
    }
  }
  return result;
}

function parseSeconds(value, field) {
  const text = typeof value === "number" || typeof value === "string" ? String(value).trim().replace(/s$/i, "") : "";
  if (!/^[1-9]\d{0,3}$/.test(text)) throw new Error(field + " must be a positive integer number of seconds");
  return Number(text);
}

function parseResolution(value, field) {
  const text = typeof value === "string" ? value.trim().toLowerCase() : "";
  if (ALL_RESOLUTIONS.includes(text)) return text;
  // WIDTHxHEIGHT is reduced to its short side.
  const match = /^([1-9]\d{2,4})[x*]([1-9]\d{2,4})$/.exec(text);
  const tier = match && { 480: "480p", 720: "720p", 1080: "1080p", 2160: "4k" }[Math.min(Number(match[1]), Number(match[2]))];
  if (!tier) throw new Error(field + " must be one of " + ALL_RESOLUTIONS.join(", ") + " or a matching WIDTHxHEIGHT");
  return tier;
}

function parseRatio(value, field) {
  if (typeof value !== "string" || !RATIOS.includes(value.trim())) throw new Error(field + " must be one of " + RATIOS.join(", "));
  return value.trim();
}

function parseBoolean(value, field) {
  if (value === true || value === "true") return true;
  if (value === false || value === "false") return false;
  throw new Error(field + " must be true or false");
}

function referenceURL(kind, value, field) {
  const url = typeof value === "string" ? value.trim() : "";
  // Images may also be inline data URIs or Bytefor face-library ids; video and audio
  // must be fetchable URLs because their durations may be measured for pricing.
  if (httpURL(url) || (kind.name === "image" && (/^data:image\/[a-z0-9.+-]+;base64,/i.test(url) || /^face:\S+$/i.test(url)))) return url;
  throw new Error(field + " must be a public http(s) URL" + (kind.name === "image" ? ", an image data URI or a face: id" : ""));
}

// Ark content: one text entry plus image_url/video_url/audio_url entries with optional roles.
function arkContent(content, field) {
  if (!Array.isArray(content)) throw new Error(field + " must be an array");
  const texts = [];
  const references = [];
  for (const item of content) {
    if (!isObject(item)) throw new Error(field + " entries must be objects");
    if (item.type === "text") {
      if (typeof item.text !== "string") throw new Error(field + " text entries must carry text");
      if (item.text.trim()) texts.push(item.text);
      continue;
    }
    if (item.type === "draft_task") throw new Error("draft_task is not supported; resubmit the draft as a new request");
    const kind = REFERENCE_KINDS.find(function (candidate) { return candidate.type === item.type; });
    if (!kind) throw new Error(field + " supports only text, image_url, video_url and audio_url entries");
    const media = item[kind.type];
    const url = referenceURL(kind, isObject(media) ? media.url : media, field + " " + kind.type + ".url");
    const reference = { type: kind.type };
    if (has(item, "role")) {
      if (!kind.roles.includes(item.role)) throw new Error(field + " " + kind.type + " role must be one of " + kind.roles.join(", "));
      reference.role = item.role;
    }
    reference[kind.type] = { url };
    references.push(reference);
  }
  return { texts, references };
}

// Decode, reservation, scheduling and submission share these validated values. The
// result is accepted again as input, because decoders and driver hooks re-run it.
function videoRequest(input, profile) {
  if (!isObject(input)) throw new Error("video request must be a JSON object");
  for (const name of Object.keys(input)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  if (has(input, "n") && input.n !== 1 && input.n !== "1") throw new Error("n must be 1");
  const metadata = has(input, "metadata") ? input.metadata : {};
  if (!isObject(metadata)) throw new Error("metadata must be an object");
  for (const name of Object.keys(metadata)) {
    if (!METADATA_FIELDS.includes(name)) throw new Error("unsupported metadata parameter: " + name);
  }
  if (has(input, "content") && has(metadata, "content")) throw new Error("send content or metadata.content, not both");
  const content = arkContent(has(input, "content") ? input.content : has(metadata, "content") ? metadata.content : [], "content");

  const sources = [input, metadata];
  const seconds = aliased(sources, ["duration", "seconds"], parseSeconds);
  const duration = seconds === undefined ? DEFAULT_SECONDS : seconds;
  if (duration < MIN_SECONDS || duration > profile.maxSeconds) throw new Error("duration must be between " + MIN_SECONDS + " and " + profile.maxSeconds + " seconds for this model");
  let resolution = aliased(sources, ["resolution"], parseResolution);
  const ratio = aliased(sources, ["ratio", "aspect_ratio"], parseRatio);
  if (has(input, "size")) {
    const size = parseResolution(input.size, "size");
    if (resolution !== undefined && resolution !== size) throw new Error("size conflicts with resolution");
    resolution = size;
  }
  if (resolution === undefined) resolution = DEFAULT_RESOLUTION;
  if (!profile.resolutions.includes(resolution)) throw new Error("resolution must be one of " + profile.resolutions.join(", ") + " for this model");

  let prompt = typeof input.prompt === "string" ? input.prompt : "";
  if (has(input, "prompt") && typeof input.prompt !== "string") throw new Error("prompt must be a string");
  if (prompt.trim() && content.texts.length) throw new Error("send the prompt once, either as prompt or as a content text entry");
  if (!prompt.trim()) prompt = content.texts.join("\n");

  // Plain URL lists are reference media. Multi-gateway clients mirror the same
  // references into content; the copies must agree so no paid reference is dropped.
  let references = content.references;
  for (const kind of REFERENCE_KINDS) {
    if (!has(input, kind.list)) continue;
    const list = input[kind.list];
    if (!Array.isArray(list)) throw new Error(kind.list + " must be an array of URLs");
    const urls = list.map(function (url) { return referenceURL(kind, url, kind.list + " entries"); });
    const inContent = references.filter(function (item) { return item.type === kind.type; }).map(function (item) { return item[kind.type].url; });
    if (inContent.length) {
      if (inContent.join("\n") !== urls.join("\n")) throw new Error(kind.list + " disagrees with the " + kind.type + " entries in content; send one list of references");
      continue;
    }
    references = references.concat(urls.map(function (url) {
      const item = { type: kind.type, role: kind.roles[kind.roles.length - 1] };
      item[kind.type] = { url };
      return item;
    }));
  }
  for (const [name, role] of [["first_image", "first_frame"], ["last_image", "last_frame"]]) {
    let url;
    for (const field of [name + "_url", name]) {
      if (!has(input, field)) continue;
      const value = referenceURL(REFERENCE_KINDS[0], input[field], field);
      if (url !== undefined && url !== value) throw new Error(name + " conflicts with " + name + "_url");
      url = value;
    }
    if (url === undefined) continue;
    const frames = references.filter(item => item.role === role);
    if (frames.length > 1 || (frames.length && frames[0].image_url.url !== url)) throw new Error(name + " conflicts with content " + role);
    if (!frames.length) references = references.concat({ type: "image_url", role, image_url: { url } });
  }
  for (const kind of REFERENCE_KINDS) {
    if (references.filter(function (item) { return item.type === kind.type; }).length > kind.max) throw new Error("at most " + kind.max + " " + kind.name + " references are allowed");
  }
  if (!prompt.trim() && !references.length) throw new Error("prompt or at least one image, video or audio reference is required");

  const output = { prompt, duration, resolution };
  if (ratio !== undefined) output.ratio = ratio;
  for (const name of ["generate_audio", "return_last_frame"]) {
    const value = aliased(sources, [name], parseBoolean);
    if (value !== undefined) output[name] = value;
  }
  if (references.length) output.content = references;
  return output;
}

function referenceURLs(params, kind) {
  return (params.content || []).filter(function (item) { return item.type === kind.type; }).map(function (item) { return item[kind.type].url; });
}

function baseURL(ctx) {
  if (typeof ctx.baseUrl !== "string" || !/^https?:\/\/[^\s/?#@\\]+(?:\/[^\s?#\\]*)?$/i.test(ctx.baseUrl)) throw new Error("channel Base URL must be an http(s) address without query, fragment or credentials");
  return ctx.baseUrl.replace(/\/+$/, "");
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  const token = typeof credential === "string" ? credential.trim().replace(/^Bearer\s+/i, "").trim() : "";
  if (!token || /\s/.test(token)) throw new Error("channel API key is required and must not contain whitespace");
  return { Authorization: "Bearer " + token };
}

function failureReason(body) {
  if (!isObject(body)) return "video generation failed";
  for (const value of [body.error, body.data && body.data.error]) {
    if (isObject(value) && typeof value.message === "string" && value.message) return value.message;
    if (typeof value === "string" && value) return value;
  }
  for (const value of [body.message, body.msg, body.fail_reason]) {
    if (typeof value === "string" && value && !/^(ok|success)$/i.test(value)) return value;
  }
  return "video generation failed";
}

function taskResult(body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "upstream task response is not a JSON object" };
  const raw = typeof body.status === "string" ? body.status.trim() : "";
  const status = { queued: "QUEUED", pending: "QUEUED", running: "IN_PROGRESS", processing: "IN_PROGRESS", succeeded: "SUCCESS", failed: "FAILURE", expired: "FAILURE", cancelled: "FAILURE" }[raw.toLowerCase()];
  if (!status) return { status: "UNKNOWN", reason: "unrecognized video task status: " + raw };
  const result = { status };
  if (status === "SUCCESS") result.progress = "100%";
  if (status === "FAILURE") result.reason = (raw.toLowerCase() === "cancelled" ? "cancelled: " : "") + failureReason(body);
  return result;
}

// Bytefor returns content.video_url as {url}; official Ark returns a string.
function videoURL(body) {
  if (!isObject(body)) return "";
  const content = isObject(body.content) ? body.content : {};
  const nested = isObject(content.video_url) ? content.video_url.url : content.video_url;
  for (const candidate of [nested, body.video_url]) {
    if (httpURL(candidate)) return candidate.trim();
  }
  return "";
}

export function buildSubmitRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (typeof model !== "string" || !model.trim()) throw new Error("upstream model is required");
  const params = videoRequest(ctx.requestBody, videoProfile(ctx));
  const body = { model: model.trim(), content: [] };
  if (params.prompt.trim()) body.content.push({ type: "text", text: params.prompt });
  body.content = body.content.concat(params.content || []);
  // resolution and duration are always explicit so the upstream bill matches the reservation.
  for (const name of ["resolution", "ratio", "duration", "generate_audio", "return_last_frame"]) {
    if (has(params, name)) body[name] = params[name];
  }
  return {
    url: baseURL(ctx) + "/api/v3/contents/generations/tasks",
    method: "POST",
    headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json" }),
    body,
  };
}

export function extractUsage(ctx) {
  const params = videoRequest(ctx.requestBody, videoProfile(ctx));
  return { seconds: params.duration, resolution: params.resolution, video_input: referenceURLs(params, REFERENCE_KINDS[1]).length > 0 };
}

// Scheduling facts for the host's purchase-cost quote; never billing usage.
export function describeSpec(ctx) {
  const params = videoRequest(ctx.requestBody, videoProfile(ctx));
  const [images, videos, audios] = REFERENCE_KINDS.map(function (kind) { return referenceURLs(params, kind); });
  // first_frame/last_frame condition the output; the host prices them as the "frame" kind, not as reference images.
  const frames = (params.content || []).filter(function (item) { return item.type === "image_url" && (item.role === "first_frame" || item.role === "last_frame"); }).length;
  return {
    spec_version: 3,
    output_seconds: params.duration,
    seconds_kind: "exact",
    resolution: params.resolution,
    references: { video: videos.length, image: images.length - frames, audio: audios.length, frame: frames },
    reference_video_urls: videos,
  };
}

// Attributes a failure reason for channel health: content or input rejections are
// the user's, the marker written for a cancelled status is neutral, the rest upstream.
export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/^cancelled: |cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/timeout|timed out|service (is temporarily )?unavailable|internal (server )?error|quota (exhausted|exceeded)|insufficient (quota|balance)|rate limit|connection|authentication|超时|服务(暂)?不可用|内部错误|余额不足|额度(不足|耗尽)|限流|并发/.test(text)) return "upstream";
  if (/content policy|content violation|moderation|sensitive|审核(未通过|不通过|拒绝)|未通过(内容)?审核|违规|敏感|invalid (image|video|audio|prompt|url)\b|unsupported (image|video|audio) (format|type)\b|出于肖像保护考虑，未认证人脸暂不支持/.test(text)) return "user";
  return "upstream";
}

// The query response carries no measured usage; keep the validated submission facts.
export function extractUsageOnComplete() {
  return {};
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("upstream create response must be a JSON object");
  if (body.id != null && typeof body.id !== "string") throw new Error("upstream create response has an invalid task id");
  const taskId = typeof body.id === "string" ? body.id.trim() : "";
  if (!taskId) {
    // Only an explicit error without a task id proves no work was accepted.
    if (body.error || body.status === "failed" || (typeof body.code === "number" && body.code !== 0)) return { rejected: { reason: failureReason(body) } };
    throw new Error("upstream create response has no task id");
  }
  const output = { taskId, taskData: body };
  const result = taskResult(body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId) throw new Error("upstream task id is required");
  return { url: baseURL(ctx) + "/api/v3/contents/generations/tasks/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(_ctx, body) {
  return taskResult(body);
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const url = videoURL(ctx.data);
  if (!url) throw new Error("completed Bytefor task has no downloadable video URL");
  // The official host validates credentialless download URLs with its SSRF guard.
  return { url, method: ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET", credentialless: true };
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("Bytefor accepts a JSON body with public reference URLs; upload files to storage first");
      const input = ctx.body.value;
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      const model = input.model.trim();
      if (ctx.model && model !== ctx.model) throw new Error("model does not match the selected channel model");
      const requestBody = videoRequest(input, videoProfile({ upstreamModel: ctx.upstreamModel, model }));
      return { kind: "submit", model, action: requestBody.content ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      if (!isObject(task.data)) return {};
      const output = {};
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      if (url) { output.video_url = url; output.url = url; }
      if (task.status === "FAILURE") output.error = { message: taskResult(task.data).reason || failureReason(task.data) };
      return output;
    },
  },
};
