// Yaohuo's supplied Open API guide and public /js/models.js + /js/app.js,
// checked 2026-10-29. The user selected the live catalog's 4–15s range for
// Seedance 2.0 over the guide's conflicting fixed-15s statement.
const MODELS = {
  "seedance-2.5": { maxSeconds: 30, resolutions: ["480p", "720p", "1080p"], images: 30, videos: 10, audios: 10 },
  "seedance-2.0": { maxSeconds: 15, resolutions: ["480p", "720p", "1080p"], images: 9, videos: 3, audios: 3 },
  "seedance-2.0-fast": { maxSeconds: 15, resolutions: ["480p", "720p"], images: 9, videos: 3, audios: 3 },
  "seedance-2.0-mini": { maxSeconds: 15, resolutions: ["480p", "720p"], images: 9, videos: 3, audios: 3 },
};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "9:21", "adaptive"];
const MEDIA = [
  { names: ["images", "reference_image_urls"], wire: "reference_image_urls", limit: "images", max: 30 },
  { names: ["videos", "reference_video_urls"], wire: "reference_video_urls", limit: "videos", max: 10 },
  { names: ["audios", "reference_audio_urls"], wire: "reference_audio_urls", limit: "audios", max: 10 },
];
const MEDIA_FIELDS = MEDIA.flatMap(group => group.names);
const PARAMETERS = ["prompt", "seconds", "duration", "resolution", "size", "ratio", "aspect_ratio", "n", "generate_audio", "mode", "first_image_url", "input_reference", "last_image_url", "last_frame"].concat(MEDIA_FIELDS);
const FIELDS = ["model", "metadata"].concat(PARAMETERS);
const SECONDS_USAGE = { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } };
const RESOLUTION_DESCRIPTION = { en: "Output video resolution", zh: "输出视频分辨率" };

export const meta = {
  apiVersion: 1,
  key: "yaohuo",
  name: "妖火AI Seedance",
  version: "1.0.0",
  author: { name: "jiayi-1994" },
  icon: "text:妖火",
  website: "https://fenglang.cloud",
  baseUrl: "https://fenglang.cloud/open/v1",
  description: { en: "Seedance video generation with reference media and first/last frames through Yaohuo AI", zh: "通过妖火AI 使用参考素材和首尾帧生成 Seedance 视频" },
  models: Object.keys(MODELS),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: {
    seconds: SECONDS_USAGE,
    resolution: { enum: ["480p", "720p", "1080p"], description: RESOLUTION_DESCRIPTION },
  },
  usageExamples: [{ label: "720p · 5s", facts: { seconds: 5, resolution: "720p" } }],
  usageProfiles: [{
    models: ["seedance-2.0-fast", "seedance-2.0-mini"],
    schema: { seconds: SECONDS_USAGE, resolution: { enum: ["480p", "720p"], description: RESOLUTION_DESCRIPTION } },
    examples: [{ label: "720p · 5s", facts: { seconds: 5, resolution: "720p" } }],
  }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function publicHTTPS(value) {
  if (typeof value !== "string" || value.length > 8192 || /[\s\\\u0000-\u001f\u007f]/.test(value)) return false;
  const match = /^https:\/\/([a-z0-9.-]+)(?::([0-9]{1,5}))?(?:[/?#].*)?$/i.exec(value);
  if (!match || (match[2] && (Number(match[2]) < 1 || Number(match[2]) > 65535))) return false;
  const host = match[1].toLowerCase().replace(/\.$/, "");
  if (!host.includes(".") || /(?:^|\.)(?:localhost|local|internal|lan)$/.test(host)) return false;
  if (/^[0-9.]+$/.test(host)) {
    const parts = host.split(".");
    if (parts.length !== 4 || parts.some(part => !/^(0|[1-9]\d{0,2})$/.test(part) || Number(part) > 255)) return false;
    const a = Number(parts[0]), b = Number(parts[1]);
    if (a === 0 || a === 10 || a === 127 || a >= 224 || (a === 169 && b === 254) || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 100 && b >= 64 && b <= 127)) return false;
  } else if (!/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?\.[a-z]{2,}$/i.test(host)) return false;
  return true;
}

function mediaURL(value) {
  if (!publicHTTPS(value)) throw new Error("reference media must be public HTTPS URLs without credentials; local files are unsupported");
  return value;
}

function seconds(value) {
  // The provider's 30-second cap is stricter than MaxTaskDurationSeconds.
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+$/.test(String(value))) throw new Error("seconds must be an integer between 4 and 30");
  const number = Number(value);
  if (!Number.isInteger(number) || number < 4 || number > 30) throw new Error("seconds must be an integer between 4 and 30");
  return number;
}

function resolution(value) {
  if (typeof value !== "string" || !["480p", "720p", "1080p"].includes(value.toLowerCase())) throw new Error("resolution must be 480p, 720p or 1080p");
  return value.toLowerCase();
}

// Equal aliases denote the same field; conflicts fail instead of dropping media.
// Repeated URLs inside an array remain separate, ordered reference inputs.
function alias(sources, names, parse) {
  let selected;
  for (const source of sources) {
    for (const name of names) {
      if (!has(source, name)) continue;
      const value = parse(source[name]);
      if (selected !== undefined && JSON.stringify(selected) !== JSON.stringify(value)) throw new Error("conflicting values for " + names.join("/"));
      selected = value;
    }
  }
  return selected;
}

// All surfaces, billing, scheduling and submission use this validation chain.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const field of Object.keys(input)) {
    if (!FIELDS.includes(field)) throw new Error("unsupported video parameter: " + field);
  }
  const sources = [input];
  if (has(input, "metadata")) {
    if (!isObject(input.metadata)) throw new Error("metadata must be an object");
    for (const field of Object.keys(input.metadata)) {
      if (!PARAMETERS.includes(field)) throw new Error("unsupported metadata parameter: " + field);
    }
    sources.push(input.metadata);
  }
  const prompt = alias(sources, ["prompt"], value => {
    if (typeof value !== "string" || !value.trim()) throw new Error("prompt is required");
    return value.trim();
  });
  if (!prompt) throw new Error("prompt is required");
  alias(sources, ["n"], value => {
    if (value !== 1) throw new Error("n must be 1");
    return value;
  });
  const duration = alias(sources, ["seconds", "duration"], seconds);
  let tier = alias(sources, ["resolution"], resolution);
  let ratio = alias(sources, ["ratio", "aspect_ratio"], value => {
    if (!RATIOS.includes(value)) throw new Error("unsupported aspect ratio");
    return value;
  });
  const size = alias(sources, ["size"], value => {
    if (typeof value !== "string") throw new Error("size must be a resolution tier or WIDTHxHEIGHT");
    return value.toLowerCase();
  });
  if (size !== undefined) {
    const dimensions = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(size);
    let sizeTier;
    if (dimensions) {
      const width = Number(dimensions[1]), height = Number(dimensions[2]);
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(value => {
        const parts = value.split(":");
        return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01;
      });
      if (!sizeRatio) throw new Error("size has an unsupported aspect ratio");
      if (ratio !== undefined && ratio !== sizeRatio) throw new Error("size conflicts with ratio");
      ratio = sizeRatio;
    } else sizeTier = resolution(size);
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    tier = sizeTier;
  }
  const request = { prompt, seconds: duration === undefined ? 5 : duration, resolution: tier || "720p", aspect_ratio: ratio || "16:9" };
  const audio = alias(sources, ["generate_audio"], value => {
    if (typeof value !== "boolean") throw new Error("generate_audio must be a boolean");
    return value;
  });
  // Public app.js submits this exact field; no default is injected here.
  if (audio !== undefined) request.generate_audio = audio;
  for (const group of MEDIA) {
    const urls = alias(sources, group.names, value => {
      if (!Array.isArray(value) || value.length > group.max) throw new Error(group.wire + " must be a URL array with at most " + group.max + " items");
      return value.map(mediaURL);
    });
    if (urls && urls.length) request[group.wire] = urls;
  }
  const first = alias(sources, ["first_image_url", "input_reference"], mediaURL);
  const last = alias(sources, ["last_image_url", "last_frame"], mediaURL);
  const hasReferences = MEDIA.some(group => !!request[group.wire]);
  if (last && !first) throw new Error("last frame requires a first frame");
  if (first && hasReferences) throw new Error("first/last frames cannot be combined with reference images, videos or audio");
  if (first) request.input_reference = first;
  if (last) request.last_frame = last;
  request.mode = hasReferences ? "reference" : last ? "firstlast" : first ? "i2v" : "t2v";
  const mode = alias(sources, ["mode"], value => {
    if (!["t2v", "i2v", "firstlast", "reference"].includes(value)) throw new Error("unsupported video mode");
    return value;
  });
  if (mode !== undefined && mode !== request.mode) throw new Error("mode conflicts with reference media or first/last frames");
  if (request.aspect_ratio === "adaptive" && request.mode === "t2v") throw new Error("adaptive ratio requires reference media or a first frame");
  return request;
}

function modelRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (!has(MODELS, model)) throw new Error("unsupported Yaohuo model: " + model);
  const profile = MODELS[model];
  const request = videoRequest(ctx.requestBody);
  if (request.seconds > profile.maxSeconds) throw new Error(model + " requires 4–" + profile.maxSeconds + " seconds");
  if (!profile.resolutions.includes(request.resolution)) throw new Error(model + " does not support " + request.resolution);
  for (const group of MEDIA) {
    if ((request[group.wire] || []).length > profile[group.limit]) throw new Error(model + " allows at most " + profile[group.limit] + " " + group.limit);
  }
  // Media durations cannot be established by synchronous JS hooks. Yaohuo
  // validates each clip and the aggregate duration before charging; never use
  // client-reported durations as billing facts. describeSpec exposes all URLs
  // for the host's protected input-video measurement when pricing requires it.
  return { model, request };
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string" || !credential.trim()) throw new Error("channel API key is required");
  const token = credential.trim().replace(/^Bearer\s+/i, "");
  if (!token || /\s/.test(token)) throw new Error("invalid channel API key");
  return { Authorization: "Bearer " + token };
}

function apiBase(ctx) {
  if (typeof ctx.baseUrl !== "string" || !ctx.baseUrl.trim()) throw new Error("channel Base URL is required");
  const base = ctx.baseUrl.replace(/\/+$/, "");
  // Accept either the documented API base or a bare site origin.
  return base.endsWith("/open/v1") ? base : base + "/open/v1";
}

function taskPath(ctx, id) {
  if (typeof id !== "string" || !id.trim()) throw new Error("upstream task id is required");
  return apiBase(ctx) + "/tasks/" + encodeURIComponent(id);
}

export function buildSubmitRequest(ctx) {
  const normalized = modelRequest(ctx);
  return { url: apiBase(ctx) + "/videos", method: "POST", headers: { ...authHeaders(ctx), "Content-Type": "application/json" }, body: { model: normalized.model, ...normalized.request } };
}

export function extractUsage(ctx) {
  const request = modelRequest(ctx).request;
  return { seconds: request.seconds, resolution: request.resolution };
}

export function extractUsageOnComplete() {
  // These models charge the submitted seconds. Wallet deductions/refunds and
  // echoed payload fields are not authoritative completion usage quantities.
  return {};
}

export function describeSpec(ctx) {
  const request = modelRequest(ctx).request;
  return {
    spec_version: 3, output_seconds: request.seconds, seconds_kind: "exact", resolution: request.resolution,
    references: { image: (request.reference_image_urls || []).length, video: (request.reference_video_urls || []).length, audio: (request.reference_audio_urls || []).length, frame: Number(!!request.input_reference) + Number(!!request.last_frame) },
    reference_video_urls: request.reference_video_urls || [],
  };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  // Only an explicit business rejection proves that no paid work was accepted.
  if (isObject(body) && body.success === false && !body.task) {
    if (typeof body.message !== "string" || !body.message.trim()) throw new Error("unrecognized Yaohuo rejection");
    return { rejected: { reason: (typeof body.code === "string" && body.code ? body.code + ": " : "") + body.message } };
  }
  if (!isObject(body) || body.success !== true || !isObject(body.task) || typeof body.task.id !== "string" || !body.task.id.trim()) throw new Error("upstream response is missing an accepted task id");
  const output = { taskId: body.task.id, taskData: body };
  const result = parseTaskResult({ taskId: body.task.id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  return { url: taskPath(ctx, ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || body.success !== true || !isObject(body.task) || typeof body.task.id !== "string" || !body.task.id.trim() || body.task.id !== ctx.taskId) return { status: "UNKNOWN", reason: "invalid or mismatched Yaohuo task response" };
  const task = body.task;
  if (typeof task.done !== "boolean" || !["queued", "in_progress", "completed", "failed"].includes(task.status)) return { status: "UNKNOWN", reason: "unrecognized Yaohuo task status" };
  if (!task.done) {
    if (task.status === "failed") return { status: "UNKNOWN", reason: "conflicting Yaohuo failure state" };
    // Upstream generation may be completed while Yaohuo still retrieves media.
    return { status: task.status === "queued" ? "QUEUED" : "IN_PROGRESS", progress: "0%" };
  }
  if (task.status === "failed") {
    const error = isObject(task.error) ? task.error.message : task.error;
    return { status: "FAILURE", reason: typeof error === "string" && error.trim() ? error : "video generation failed" };
  }
  if (task.status !== "completed") return { status: "UNKNOWN", reason: "conflicting Yaohuo completion state" };
  // Even without videoUrl, the authenticated /content endpoint is documented
  // to serve the completed video. Do not mark it failed for a missing CDN URL.
  return { status: "SUCCESS", progress: "100%" };
}

export function listArtifacts(task) {
  const result = isObject(task.data) && isObject(task.data.task) ? task.data.task : null;
  return task.status === "SUCCESS" && result && result.done === true && result.status === "completed" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const task = isObject(ctx.data) && isObject(ctx.data.task) ? ctx.data.task : null;
  if (ctx.artifactKey !== "video" || !task || task.done !== true || task.status !== "completed") throw new Error("artifact_not_found");
  const method = ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET";
  // Always use the durable authenticated endpoint. videoUrl may expire after
  // seven days; returned downloadUrl/contentUrl must never receive the key.
  // The host handles Range and cross-origin redirects without forwarding keys.
  return { url: taskPath(ctx, ctx.upstreamTaskId || ctx.taskId) + "/content", method, headers: authHeaders(ctx) };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/content moderation|content policy violation|内容.{0,12}(违规|敏感|不合规)/.test(text)) return "user";
  if (/^(?:bad_ratio|missing_prompt|invalid_reference|invalid_seconds):/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest(ctx) {
      const body = ctx.body;
      let input;
      if (body && body.kind === "json") input = body.value;
      else if (body && (body.kind === "multipart" || body.kind === "form")) {
        if ((body.files || []).length) throw new Error("Yaohuo requires public HTTPS URLs; local file uploads are unsupported");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length || entries.some(value => typeof value !== "string")) throw new Error(name + " requires a string value");
          if (MEDIA_FIELDS.includes(name)) {
            if (entries.length === 1 && entries[0].trim().startsWith("[")) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be a URL array"); }
            } else input[name] = entries.slice();
          } else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            if (["metadata", "generate_audio", "n"].includes(name)) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be valid JSON"); }
            } else input[name] = entries[0];
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Channel mapping is authoritative; aliases can be decoded before a
      // channel is selected, then validated against its actual upstream model.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody });
      return { kind: "submit", model: input.model, action: requestBody.mode === "t2v" ? "text_to_video" : "reference_to_video", requestBody };
    },
    render() {
      // The host supplies public IDs and its content URL. Never publish the
      // provider's account data, private task ID or authenticated pickup URLs.
      return {};
    },
  },
};
