// Task Plugin API v1 for https://api.onmi.eu.cc/docs (E group).
const MODELS = ["e组-sd2.0mini", "e组-sd2.0fast", "e组-sd2.0", "e组-sd2.5","g组-sd2.0mini", "g组-sd2.0fast", "g组-sd2.0", "g组-sd2.5"];
const RESOLUTIONS = ["480p", "720p", "1080p"];
const RATIOS = ["21:9", "16:9", "4:3", "1:1", "3:4", "9:16"];
const FIELDS = ["model", "prompt", "seconds", "duration", "resolution", "size", "aspect_ratio", "ratio", "n", "generate_audio", "references", "images", "videos", "audios"];

export const meta = {
  apiVersion: 1,
  key: "onmi",
  name: "Onmi E Video",
  version: "1.0.1",
  author: { name: "jiayi-1994" },
  description: { en: "E group video generation through the Onmi API", zh: "通过 Onmi API 生成 E 组视频" },
  icon: "text:ON",
  website: "https://api.onmi.eu.cc/docs",
  baseUrl: "https://api.onmi.eu.cc",
  models: MODELS,
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: {
    output_seconds: { type: "number", unit: "second", description: { en: "Output video generation unit price", zh: "输出视频生成单价" } },
    resolution: { enum: RESOLUTIONS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
    video_input: { type: "boolean", description: { en: "Reference video present", zh: "存在参考视频" } },
  },
  usageExamples: [{ label: "480p · 10s", facts: { output_seconds: 10, resolution: "480p", video_input: false } }],
};

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function alias(input, names, normalize) {
  let value;
  for (const name of names) {
    if (input[name] === undefined) continue;
    const next = normalize(input[name]);
    if (value !== undefined && value !== next) throw new Error(names.join(" and ") + " conflict");
    value = next;
  }
  return value;
}

function seconds(value) {
  if (!["number", "string"].includes(typeof value) || !/^\d+$/.test(String(value))) throw new Error("seconds must be an integer from 4 to 30");
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 4 || parsed > 30) throw new Error("seconds must be an integer from 4 to 30");
  return parsed;
}

function resolution(value) {
  if (typeof value !== "string" || !RESOLUTIONS.includes(value)) throw new Error("resolution must be 480p, 720p or 1080p");
  return value;
}

function ratio(value) {
  if (typeof value !== "string" || !RATIOS.includes(value)) throw new Error("unsupported aspect_ratio");
  return value;
}

// All driver hooks revalidate the final mapped request, including parameter overrides.
function videoRequest(input, model) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const name of Object.keys(input)) {
    if (!FIELDS.includes(name)) throw new Error(name + " is not supported by Onmi E group");
  }
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  const duration = alias(input, ["seconds", "duration"], seconds);
  const tier = alias(input, ["resolution", "size"], resolution);
  if (duration === undefined || tier === undefined) throw new Error("seconds and resolution are required");
  const aspect = alias(input, ["aspect_ratio", "ratio"], ratio) || "16:9";
  if (input.n !== undefined && input.n !== 1) throw new Error("n must be 1");
  if (input.generate_audio !== undefined && typeof input.generate_audio !== "boolean") throw new Error("generate_audio must be a boolean");
  if (model && !MODELS.includes(model)) throw new Error("unsupported upstream video model: " + model);
  const large = !model || model === "e组-sd2.5";
  const maximum = large ? 30 : 15;
  if (duration > maximum) throw new Error("seconds exceeds the model limit of " + maximum);
  if (!large && tier === "1080p") throw new Error("this model supports only 480p and 720p");

  const suppliedReferences = input.references === undefined ? [] : input.references;
  if (!Array.isArray(suppliedReferences)) throw new Error("references must be an array");
  const references = suppliedReferences.slice();
  // Normalize host media arrays before validation so submission and scheduling
  // use the same references. Preserve repeated inputs and native metadata.
  for (const [field, type] of [["images", "image"], ["videos", "video"], ["audios", "audio"]]) {
    const urls = input[field];
    if (urls === undefined) continue;
    if (!Array.isArray(urls) || !urls.every(url => typeof url === "string")) throw new Error(field + " must be an array of public HTTP(S) URLs");
    for (const url of urls) references.push({ type, url });
  }
  if (references.length > (large ? 50 : 15)) throw new Error("too many references");
  const counts = { image: 0, video: 0, audio: 0 };
  const totals = { video: 0, audio: 0 };
  const normalized = references.map(function (reference) {
    if (!isObject(reference) || !["image", "video", "audio"].includes(reference.type)) throw new Error("reference type must be image, video or audio");
    for (const name of Object.keys(reference)) {
      if (!["type", "url", "name", "duration"].includes(name)) throw new Error("unsupported reference field: " + name);
    }
    // Media is fetched by the upstream; enforce its public URL syntax here.
    const url = reference.url;
    const match = typeof url === "string" && /^(https?):\/\/([a-z0-9.-]+)(?::(\d+))?(?:[/?][^\s\\#]*)?$/i.exec(url);
    if (!match || url.length > 4096 || (match[3] && match[3] !== (match[1].toLowerCase() === "https" ? "443" : "80"))) throw new Error("references require public HTTP(S) URLs on port 80 or 443 without credentials");
    const host = match[2].toLowerCase();
    if (host === "localhost" || host.endsWith(".localhost") || /^(127|10|0)\.|^169\.254\.|^192\.168\.|^172\.(1[6-9]|2\d|3[01])\./.test(host)) throw new Error("reference URL must be public");
    const item = { type: reference.type, url };
    if (reference.name !== undefined) {
      if (typeof reference.name !== "string") throw new Error("reference name must be a string");
      item.name = reference.name;
    }
    if (reference.duration !== undefined) {
      if (reference.type === "image" || typeof reference.duration !== "number" || !Number.isFinite(reference.duration) || reference.duration < 2 || reference.duration > maximum) throw new Error("video/audio reference duration must be between 2 and " + maximum);
      item.duration = reference.duration;
      totals[reference.type] += reference.duration;
      if (totals[reference.type] > maximum) throw new Error("total " + reference.type + " reference duration exceeds " + maximum);
    }
    counts[reference.type]++;
    const limit = large ? (reference.type === "image" ? 30 : 10) : (reference.type === "image" ? 9 : 3);
    if (counts[reference.type] > limit) throw new Error("too many " + reference.type + " references");
    return item;
  });
  if (!large && counts.audio && !counts.image && !counts.video) throw new Error("audio references require an image or video for this model");
  const body = { prompt: input.prompt, seconds: duration, resolution: tier, aspect_ratio: aspect, generate_audio: input.generate_audio === undefined ? true : input.generate_audio, references: normalized };
  if (model) body.model = model;
  return body;
}

function mappedRequest(ctx) {
  return videoRequest(ctx.requestBody, ctx.upstreamModel || ctx.model);
}

function baseURL(ctx) {
  return ctx.baseUrl.replace(/\/+$/, "").replace(/\/v1$/, "");
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  const token = typeof credential === "string" ? credential.trim().replace(/^Bearer\s+/i, "") : "";
  if (!token || /\s/.test(token)) throw new Error("channel API key is required and must not contain whitespace");
  return { Authorization: "Bearer " + token };
}

export function buildSubmitRequest(ctx) {
  const body = mappedRequest(ctx);
  // A host task ID is unique across users and stable across upstream retries.
  if (typeof ctx.publicTaskId !== "string" || !ctx.publicTaskId) throw new Error("publicTaskId is required for upstream idempotency");
  return {
    url: baseURL(ctx) + "/v1/videos", method: "POST",
    headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json", "Idempotency-Key": ctx.publicTaskId }),
    body,
  };
}

export function extractUsage(ctx) {
  const body = mappedRequest(ctx);
  // Local output-based sales facts, NOT the vendor's input-video procurement meter.
  // Onmi measures reference video duration itself; caller duration is never usage.
  return { output_seconds: body.seconds, resolution: body.resolution, video_input: body.references.some(item => item.type === "video") };
}

export function extractUsageOnComplete() {
  // No documented measured-usage response field: preserve frozen local sales facts.
  return {};
}

export function describeSpec(ctx) {
  const body = mappedRequest(ctx);
  const references = { image: 0, video: 0, audio: 0 };
  for (const item of body.references) references[item.type]++;
  return { spec_version: 2, output_seconds: body.seconds, seconds_kind: "exact", resolution: body.resolution, references, reference_video_urls: body.references.filter(item => item.type === "video").map(item => item.url) };
}

export function parseTaskResult(_ctx, body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "invalid task response" };
  const statuses = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE", cancelled: "FAILURE", canceled: "FAILURE" };
  const status = Object.prototype.hasOwnProperty.call(statuses, body.status) ? statuses[body.status] : "UNKNOWN";
  const result = { status };
  if (typeof body.progress === "number" && Number.isFinite(body.progress) && body.progress >= 0 && body.progress <= 100) result.progress = body.progress + "%";
  if (status === "UNKNOWN") result.reason = "unrecognized upstream task status";
  if (status === "FAILURE") {
    const error = body.error;
    const message = isObject(error) ? error.message : error;
    result.reason = typeof message === "string" && message ? message : "video generation failed";
    if (isObject(error) && error.code === "video_request_rejected") result.reason = "video request rejected";
    if (body.status === "cancelled" || body.status === "canceled" || (isObject(error) && error.code === "video_generation_cancelled")) result.reason = "cancelled: " + result.reason;
  }
  return result;
}

export function parseSubmitResponse(ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("upstream create response must be an object");
  if (body.id !== undefined && (typeof body.id !== "string" || !body.id.trim())) throw new Error("invalid upstream task id");
  const result = parseTaskResult(ctx, body);
  if (body.id) {
    if ((body.error || body.success === false) && result.status !== "FAILURE") throw new Error("conflicting upstream acceptance and rejection signals");
    const output = { taskId: body.id, taskData: body };
    if (["SUCCESS", "FAILURE"].includes(result.status)) output.immediate = result;
    return output;
  }
  if ((body.error || body.success === false || result.status === "FAILURE") && (!body.status || result.status === "FAILURE")) {
    const error = isObject(body.error) ? body.error.message : body.error;
    return { rejected: { reason: (result.status === "FAILURE" && result.reason) || (typeof error === "string" && error) || "upstream rejected video creation" } };
  }
  throw new Error("upstream create response has no task id");
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId) throw new Error("upstream task id is required");
  return { url: baseURL(ctx) + "/v1/videos/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  if (typeof ctx.upstreamTaskId !== "string" || !ctx.upstreamTaskId) throw new Error("upstream task id is required");
  // Signed URLs may be relative/expired. The documented authenticated endpoint
  // avoids attaching the channel key to an arbitrary provider-supplied URL.
  return { url: baseURL(ctx) + "/v1/videos/" + encodeURIComponent(ctx.upstreamTaskId) + "/content", method: ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET", headers: authHeaders(ctx) };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/^cancelled: |cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/timeout|timed out|unavailable|internal.*error|quota|balance|rate limit|authentication|超时|额度|余额|限流/.test(text)) return "upstream";
  if (/^video request rejected$|content.*(policy violation|moderation.*reject)|moderation rejected|内容.*违规|审核(未通过|不通过|拒绝)/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest(ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("Onmi requires JSON with public reference URLs");
      const input = ctx.body.value;
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match selected model");
      // Candidate decode precedes channel mapping; enforce model limits in drivers.
      const body = videoRequest(input, ctx.upstreamModel);
      return { kind: "submit", model: input.model, action: body.references.length ? "reference_to_video" : "text_to_video", requestBody: body };
    },
    render(_ctx, task) {
      const output = {};
      const data = isObject(task.data) ? task.data : {};
      for (const name of ["seconds", "resolution", "aspect_ratio"]) {
        if (["string", "number"].includes(typeof data[name])) output[name] = data[name];
      }
      if (task.status === "FAILURE") output.error = { message: task.fail_reason || "video generation failed" };
      return output;
    },
  },
};
