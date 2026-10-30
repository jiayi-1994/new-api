// elevenlabs-pool 0.8.0: pool/models.py, app.py and store.py, checked 2026-10-21.
// The pool imports public media URLs; reference duration/MIME/size checks remain
// upstream. Task status and the signed result URL are separate HTTP resources.
const MODELS = {
  "bytedance-seedance-v1-pro": { min: 3, max: 12, resolutions: ["480p", "720p", "1080p"], limits: [0, 0, 0, 0], prompt: 12000, audio: false, frames: true, autoFrame: true },
  "bytedance-seedance-v1.5-pro": { min: 4, max: 12, resolutions: ["480p", "720p"], limits: [0, 0, 0, 0], prompt: 32000, audio: true, frames: true, autoFrame: false },
  "bytedance-seedance-v2": { min: 4, max: 15, resolutions: ["480p", "720p", "1080p", "4k"], limits: [9, 3, 3, 12], prompt: 32000, audio: true, frames: true, autoFrame: true },
  "bytedance-seedance-v2-fast": { min: 4, max: 15, resolutions: ["480p", "720p"], limits: [9, 3, 3, 12], prompt: 32000, audio: true, frames: true, autoFrame: true },
  "bytedance-seedance-v2-mini": { min: 4, max: 15, resolutions: ["480p", "720p"], limits: [9, 3, 3, 12], prompt: 32000, audio: true, frames: true, autoFrame: true },
  "bytedance-seedance-v2.5": { min: 4, max: 30, resolutions: ["480p", "720p", "1080p"], limits: [30, 10, 10, 50], prompt: 12000, audio: true, frames: true, autoFrame: true, followRatio: true },
  "bytedance-seedance-v2.5-edit": { resolutions: ["480p", "720p", "1080p"], limits: [30, 10, 10, 50], prompt: 12000, audio: true, frames: false, followRatio: true, videoRequired: true, edit: true },
  "bytedance-seedance-v2.5-extend": { min: 4, max: 30, resolutions: ["480p", "720p", "1080p"], limits: [30, 10, 10, 50], prompt: 12000, audio: true, frames: false, followRatio: true, videoRequired: true },
};
const ALIASES = {
  "seedance-1.0-pro": "bytedance-seedance-v1-pro",
  "seedance-1.5-pro": "bytedance-seedance-v1.5-pro",
  "seedance-2.0": "bytedance-seedance-v2",
  "seedance-2.0-fast": "bytedance-seedance-v2-fast",
  "seedance-2.0-mini": "bytedance-seedance-v2-mini",
  "seedance-2.5": "bytedance-seedance-v2.5",
  "seedance-2.5-edit": "bytedance-seedance-v2.5-edit",
  "seedance-2.5-extend": "bytedance-seedance-v2.5-extend",
};
const RATIOS = ["21:9", "16:9", "4:3", "1:1", "3:4", "9:16"];
const MEDIA = [
  { names: ["image_urls", "images"], limit: 30 },
  { names: ["video_urls", "videos"], limit: 10 },
  { names: ["audio_urls", "audios"], limit: 10 },
];
const MEDIA_FIELDS = MEDIA.flatMap(function (group) { return group.names; });
const FRAME_FIELDS = ["start_frame_url", "first_frame_url", "first_image_url", "end_frame_url", "last_frame_url", "last_image_url"];
const PARAMETERS = ["prompt", "seconds", "duration", "duration_secs", "resolution", "size", "aspect_ratio", "ratio", "generate_audio", "n", "image_url"].concat(MEDIA_FIELDS, FRAME_FIELDS);
const FIELDS = ["model", "metadata"].concat(PARAMETERS);
const CONDITIONS = {
  resolution: { enum: ["480p", "720p", "1080p", "4k"], description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  generate_audio: { type: "boolean", description: { en: "Whether audio is generated", zh: "是否生成音频" } },
};

export const meta = {
  apiVersion: 1,
  key: "elevenlabs-pool",
  name: "ElevenLabs Pool",
  version: "1.0.1",
  author: { name: "jiayi-1994" },
  icon: "text:EL",
  description: { en: "Seedance video generation through ElevenLabs Pool", zh: "通过 ElevenLabs Pool 生成 Seedance 视频" },
  models: Object.keys(MODELS).concat(Object.keys(ALIASES)),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  // No default Base URL: the pool is self-hosted and loopback may refer to a
  // different machine/container from the gateway. Configure the channel URL.
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    ...CONDITIONS,
  },
  usageExamples: [{ label: "480p · 4s", facts: { seconds: 4, resolution: "480p", generate_audio: true } }],
  // Edit follows input length (-1). The pool exposes estimates/reservations,
  // not authoritative output seconds or charged credits. Bill once per video
  // with an independently configured price; never use -1 as a quantity.
  usageProfiles: [{
    models: ["bytedance-seedance-v2.5-edit", "seedance-2.5-edit"],
    schema: {
      video_count: { type: "number", unit: "count", unitLabel: { en: "video", zh: "条" }, description: { en: "Video editing unit price", zh: "视频编辑单价" } },
      ...CONDITIONS,
    },
    examples: [{ label: "480p", facts: { video_count: 1, resolution: "480p", generate_audio: true } }],
  }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value) {
  return typeof value === "string" && /^https?:\/\/[^\s/?#@\\]+(?:[/?][^\s#\\]*)?$/i.test(value) && !/[\u0000-\u001f\u007f]/.test(value);
}

function referenceURL(value) {
  if (!httpURL(value) || value.length < 10 || value.length > 4096) throw new Error("reference must be an HTTP(S) URL without credentials or fragments, at most 4096 characters");
  const authority = /^https?:\/\/([^/?#]+)/i.exec(value)[1];
  if (!/^(?:\[[0-9a-f:]+\]|[^:]+)(?::(?:80|443))?$/i.test(authority)) throw new Error("reference URLs support only ports 80 and 443");
  return value;
}

function duration(value) {
  if ((typeof value !== "number" && typeof value !== "string") || !/^(?:-1|\d+)$/.test(String(value))) throw new Error("duration must be an integer from 3 to 30, or -1 for Video Edit");
  const result = Number(value);
  if (result !== -1 && (!Number.isInteger(result) || result < 3 || result > 30)) throw new Error("duration must be an integer from 3 to 30, or -1 for Video Edit");
  return result;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be 480p, 720p, 1080p or 4k");
  const tier = value.toLowerCase() === "2160p" ? "4k" : value.toLowerCase();
  if (!CONDITIONS.resolution.enum.includes(tier)) throw new Error("resolution must be 480p, 720p, 1080p or 4k");
  return tier;
}

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

// Used after every override as well as at the initial protocol boundary.
// Own all arrays; equal mirror fields are one list, repeated URLs stay repeated.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const name of Object.keys(input)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  const sources = [input];
  if (has(input, "metadata")) {
    if (!isObject(input.metadata)) throw new Error("metadata must be an object");
    for (const name of Object.keys(input.metadata)) {
      if (!PARAMETERS.includes(name)) throw new Error("unsupported metadata parameter: " + name);
    }
    sources.push(input.metadata);
  }
  const prompt = alias(sources, ["prompt"], function (value) {
    if (typeof value !== "string" || !value.trim() || value.length > 32000) throw new Error("prompt must contain 1..32000 characters");
    // Match Python's UTF-16 validation, including isolated surrogate rejection.
    for (let i = 0; i < value.length; i++) {
      const code = value.charCodeAt(i);
      if (code >= 0xd800 && code <= 0xdbff) {
        const next = value.charCodeAt(++i);
        if (!(next >= 0xdc00 && next <= 0xdfff)) throw new Error("prompt must contain valid Unicode");
      } else if (code >= 0xdc00 && code <= 0xdfff) throw new Error("prompt must contain valid Unicode");
    }
    return value;
  });
  if (!prompt) throw new Error("prompt is required");
  alias(sources, ["n"], function (value) {
    if (value !== 1) throw new Error("n must be 1");
    return value;
  });
  const request = { prompt };
  const seconds = alias(sources, ["seconds", "duration", "duration_secs"], duration);
  if (seconds !== undefined) request.duration_secs = seconds;
  let tier = alias(sources, ["resolution"], resolution);
  let ratio = alias(sources, ["ratio", "aspect_ratio"], function (value) {
    if (value !== "auto" && !RATIOS.includes(value)) throw new Error("unsupported aspect ratio");
    return value;
  });
  const size = alias(sources, ["size"], function (value) {
    if (typeof value !== "string") throw new Error("size must be a resolution tier or WIDTHxHEIGHT");
    return value.toLowerCase();
  });
  if (size !== undefined) {
    const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(size);
    let sizeTier;
    if (match) {
      const width = Number(match[1]), height = Number(match[2]);
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(function (value) {
        const parts = value.split(":");
        return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01;
      });
      if (!sizeRatio) throw new Error("size has an unsupported aspect ratio");
      if (ratio !== undefined && ratio !== sizeRatio) throw new Error("size conflicts with aspect ratio");
      ratio = sizeRatio;
    } else sizeTier = resolution(size);
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    tier = sizeTier;
  }
  if (tier !== undefined) request.resolution = tier;
  if (ratio !== undefined) request.aspect_ratio = ratio;
  const audio = alias(sources, ["generate_audio"], function (value) {
    if (typeof value !== "boolean") throw new Error("generate_audio must be a boolean");
    return value;
  });
  if (audio !== undefined) request.generate_audio = audio;
  for (const group of MEDIA) {
    const urls = alias(sources, group.names, function (value) {
      if (!Array.isArray(value) || value.length > group.limit) throw new Error(group.names[0] + " must be an array of at most " + group.limit + " URLs");
      return Array.from(value, referenceURL);
    });
    if (urls && urls.length) request[group.names[0]] = urls;
  }
  const image = alias(sources, ["image_url"], referenceURL);
  if (image !== undefined) {
    if (request.image_urls) throw new Error("use image_url or image_urls/images, not both");
    request.image_urls = [image];
  }
  const first = alias(sources, ["start_frame_url", "first_frame_url", "first_image_url"], referenceURL);
  const last = alias(sources, ["end_frame_url", "last_frame_url", "last_image_url"], referenceURL);
  if (last && !first) throw new Error("end frame requires a start frame");
  if (first && MEDIA.some(function (group) { return !!request[group.names[0]]; })) throw new Error("frames cannot be combined with reference images, videos or audios");
  if (first) request.start_frame_url = first;
  if (last) request.end_frame_url = last;
  return request;
}

function modelRequest(ctx) {
  const name = ctx.upstreamModel || ctx.model;
  const model = has(ALIASES, name) ? ALIASES[name] : name;
  if (!has(MODELS, model)) throw new Error("unsupported ElevenLabs Pool model: " + String(name));
  const capability = MODELS[model];
  const origin = has(ALIASES, ctx.model) ? ALIASES[ctx.model] : ctx.model;
  if (has(MODELS, origin) && !!MODELS[origin].edit !== !!capability.edit) throw new Error("Video Edit and duration-priced models cannot be mapped to each other");
  const request = videoRequest(ctx.requestBody);
  if (request.prompt.length > capability.prompt) throw new Error(model + " prompt exceeds " + capability.prompt + " characters");
  if (request.duration_secs === undefined) request.duration_secs = capability.edit ? -1 : 4;
  if (capability.edit) {
    if (request.duration_secs !== -1) throw new Error("Video Edit duration must be -1 to follow the input video");
  } else if (request.duration_secs < capability.min || request.duration_secs > capability.max) throw new Error(model + " supports duration " + capability.min + ".." + capability.max);
  request.resolution = request.resolution || "480p";
  if (!capability.resolutions.includes(request.resolution)) throw new Error(model + " supports only " + capability.resolutions.join(", "));
  let count = 0;
  for (let i = 0; i < MEDIA.length; i++) {
    const length = (request[MEDIA[i].names[0]] || []).length;
    if (length > capability.limits[i]) throw new Error(model + " allows at most " + capability.limits[i] + " " + MEDIA[i].names[0]);
    count += length;
  }
  if (count > capability.limits[3]) throw new Error(model + " allows at most " + capability.limits[3] + " total references");
  if (!capability.followRatio && request.audio_urls && !request.image_urls && !request.video_urls) throw new Error("reference audio requires an image or video");
  if (capability.videoRequired && !request.video_urls) throw new Error("Video Edit/Extend requires a reference video");
  if (request.start_frame_url && !capability.frames) throw new Error("this model does not support frames");
  if (request.generate_audio === true && !capability.audio) throw new Error("Seedance 1 Pro does not generate audio");
  // Keep omitted generate_audio absent on the wire. Usage uses the pool's
  // model-specific default; explicit false must reach the upstream unchanged.
  const followsInput = request.video_urls || (request.start_frame_url && capability.followRatio);
  // Seedance 2.5 derives framing from the input media, including when a
  // standard client sends its default ratio or a size-derived ratio.
  request.aspect_ratio = followsInput && capability.followRatio ? "auto" : request.aspect_ratio || (followsInput ? "auto" : "16:9");
  if (request.aspect_ratio === "auto" && !request.video_urls && !(request.start_frame_url && capability.autoFrame)) throw new Error("auto aspect ratio requires a supported start frame or reference video");
  return { model, request, capability };
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
  if (typeof ctx.publicTaskId !== "string" || !/^[A-Za-z0-9_-]{8,128}$/.test(ctx.publicTaskId)) throw new Error("host public task id is required for idempotent submission");
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos", method: "POST",
    headers: { ...authHeaders(ctx), "Content-Type": "application/json", "Idempotency-Key": ctx.publicTaskId },
    body: { model: normalized.model, ...normalized.request },
  };
}

export function extractUsage(ctx) {
  const normalized = modelRequest(ctx), request = normalized.request;
  return {
    ...(normalized.capability.edit ? { video_count: 1 } : { seconds: request.duration_secs }),
    resolution: request.resolution,
    generate_audio: has(request, "generate_audio") ? request.generate_audio : normalized.capability.audio,
  };
}

export function extractUsageOnComplete() {
  // quote/reserved/hold are pool quota estimates/accounting holds, and timing
  // is elapsed wall time. None is authoritative generated duration or usage.
  return {};
}

export function describeSpec(ctx) {
  const normalized = modelRequest(ctx), request = normalized.request;
  if (normalized.capability.edit) return { unsupported: true };
  return {
    spec_version: 3, output_seconds: request.duration_secs, seconds_kind: "exact", resolution: request.resolution,
    references: { image: (request.image_urls || []).length, video: (request.video_urls || []).length, audio: (request.audio_urls || []).length, frame: Number(!!request.start_frame_url) + Number(!!request.end_frame_url) },
    reference_video_urls: request.video_urls || [],
  };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  // No documented 2xx business rejection. An absent id is an unknown outcome,
  // never a reason to resubmit potentially paid work with a fresh key.
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim()) throw new Error("upstream response is missing a task id");
  const output = { taskId: body.id, taskData: body };
  const result = parseTaskResult({ taskId: body.id }, body);
  if (result.status === "FAILURE") output.immediate = result;
  if (result.state) output.state = result.state;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId.trim()) throw new Error("upstream task id is required");
  const suffix = isObject(ctx.state) && ctx.state.fetchResult === true ? "/result" : "";
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/tasks/" + encodeURIComponent(ctx.taskId) + suffix, method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim() || body.id !== ctx.taskId) return { status: "UNKNOWN", reason: "invalid or mismatched ElevenLabs Pool task id" };
  if (isObject(ctx.state) && ctx.state.fetchResult === true) {
    if (!body.error && !has(body, "status") && httpURL(body.content_url)) return { status: "SUCCESS", progress: "100%" };
    return { status: "UNKNOWN", reason: "missing or invalid ElevenLabs Pool result URL" };
  }
  if (body.status === "failed" || body.status === "cancelled") return {
    status: "FAILURE", reason: body.status === "cancelled" ? "cancelled_by_operator" : typeof body.error === "string" && body.error.trim() ? body.error : "video generation failed",
  };
  // Active pool tasks may carry diagnostic errors (CAPTCHA/account shortage/
  // unknown submission). They remain pending and must not be refunded/retried.
  if (body.status === "completed") return { status: "IN_PROGRESS", state: { fetchResult: true } };
  const statuses = { queued: "QUEUED", preparing: "IN_PROGRESS", waiting_captcha: "IN_PROGRESS", submitting: "IN_PROGRESS", submission_unknown: "IN_PROGRESS", processing: "IN_PROGRESS" };
  if (!has(statuses, body.status)) return { status: "UNKNOWN", reason: "unrecognized ElevenLabs Pool task status" };
  return { status: statuses[body.status] };
}

function videoURL(body) {
  return isObject(body) && !body.error && !has(body, "status") && httpURL(body.content_url) ? body.content_url : "";
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && videoURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const url = videoURL(ctx.data);
  if (ctx.artifactKey !== "video" || !url) throw new Error("artifact_not_found");
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (text === "cancelled_by_operator" || /cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/^internal_moderation\b|content moderation|content policy violation|内容.{0,12}(违规|敏感|不合规)/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      let input;
      if (body && body.kind === "json") input = body.value;
      else if (body && body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("ElevenLabs Pool requires media URLs; local file uploads are unsupported");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) {
            if (entries.length === 1 && entries[0].trim().startsWith("[")) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be a URL array"); }
            } else input[name] = entries;
          } else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            if (["metadata", "generate_audio"].includes(name)) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be valid JSON"); }
            } else input[name] = name === "n" && entries[0] === "1" ? 1 : entries[0];
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Initial candidate decoding precedes channel mapping. Preserve omitted
      // defaults until the final model is known, especially Edit's duration.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody });
      return { kind: "submit", model: input.model, action: requestBody.start_frame_url || MEDIA.some(function (group) { return !!requestBody[group.names[0]]; }) ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      // Account/workspace IDs, credit reservations and upstream IDs stay private.
      return url ? { url } : {};
    },
  },
};
