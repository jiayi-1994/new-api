// ChatCut2API app/service.py and ChatCut's seedance2 contract / composer bundle,
// checked 2026-10-17. Use params for media: the deployed older API ignores new
// top-level media fields, but both versions merge params into generation/submit.
const MODELS = {
  seedance2mini: { resolutions: ["480p", "720p"], maxSeconds: 15 },
  seedance2fast: { resolutions: ["480p", "720p"], maxSeconds: 15 },
  seedance2: { resolutions: ["480p", "720p", "1080p"], maxSeconds: 15 },
  "seedance-2-5": { resolutions: ["480p", "720p", "1080p"], maxSeconds: 30 },
};
const ALIASES = {
  "seedance-2.0-mini": "seedance2mini",
  "seedance-2.0-fast": "seedance2fast",
  "seedance-2.0": "seedance2",
  "seedance-2.5": "seedance-2-5",
};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
// Expose the requested 9/3/3 reference envelope on every model. Seedance 2.5's
// larger native envelope is deliberately not claimed by this plugin version.
const MEDIA = [
  { names: ["images", "reference_images", "referenceImages", "imageUrls"], limit: 9 },
  { names: ["videos", "reference_videos", "referenceVideos", "videoUrls"], limit: 3 },
  { names: ["audios", "reference_audios", "referenceAudios", "audioUrls"], limit: 3 },
];
const MEDIA_FIELDS = MEDIA.flatMap(function (group) { return group.names; });
const FRAME_FIELDS = ["first_image_url", "first_frame_url", "first_frame", "image", "last_image_url", "last_frame_url", "last_frame", "image_tail", "imageTail"];
const PARAMETERS = ["prompt", "seconds", "duration", "resolution", "size", "ratio", "aspect_ratio", "aspectRatio", "n", "generate_audio", "functionMode"].concat(MEDIA_FIELDS, FRAME_FIELDS);
const FIELDS = ["model", "metadata", "params"].concat(PARAMETERS);

export const meta = {
  apiVersion: 1,
  key: "chatcut2api",
  name: "ChatCut2API",
  version: "1.0.0",
  author: { name: "jiayi-1994" },
  icon: "text:CC",
  description: { en: "Seedance video generation with reference media and first/last frames through ChatCut2API", zh: "通过 ChatCut2API 使用参考素材和首尾帧生成 Seedance 视频" },
  baseUrl: "http://82.158.230.113:8790",
  models: Object.keys(MODELS).concat(Object.keys(ALIASES)),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    resolution: { enum: ["480p", "720p", "1080p"], description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  },
  usageExamples: [{ label: "720p · 5s", facts: { seconds: 5, resolution: "720p" } }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value) {
  return typeof value === "string" && value.length <= 8192 && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value) && !/[\u0000-\u001f\u007f]/.test(value);
}

function seconds(value) {
  // Model caps are stricter than the host MaxTaskDurationSeconds (3600).
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+$/.test(String(value))) throw new Error("seconds must be an integer between 4 and 30");
  const result = Number(value);
  if (!Number.isInteger(result) || result < 4 || result > 30) throw new Error("seconds must be an integer between 4 and 30");
  return result;
}

function resolution(value) {
  if (typeof value !== "string" || !["480p", "720p", "1080p"].includes(value.toLowerCase())) throw new Error("resolution must be 480p, 720p or 1080p");
  return value.toLowerCase();
}

function aspectRatio(value) {
  if (value !== "adaptive" && !RATIOS.includes(value)) throw new Error("unsupported aspect ratio");
  return value;
}

function imageURL(value) {
  if (!httpURL(value)) throw new Error("frame must be an HTTP(S) image URL without credentials");
  return value;
}

// Mirror fields must agree; equal aliases represent one value, while repeated
// URLs within an array remain separate inputs for both submission and quoting.
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

// One normalization chain for decode, overrides, billing, scheduling and submit.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const name of Object.keys(input)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  const sources = [input];
  for (const name of ["metadata", "params"]) {
    if (!has(input, name)) continue;
    if (!isObject(input[name])) throw new Error(name + " must be an object");
    for (const key of Object.keys(input[name])) {
      if (!PARAMETERS.includes(key)) throw new Error("unsupported " + name + " parameter: " + key);
    }
    sources.push(input[name]);
  }
  const prompt = alias(sources, ["prompt"], function (value) {
    if (typeof value !== "string" || !value.trim()) throw new Error("prompt is required");
    return value.trim();
  });
  if (!prompt) throw new Error("prompt is required");
  alias(sources, ["n"], function (value) {
    if (value !== 1) throw new Error("n must be 1");
    return value;
  });
  const duration = alias(sources, ["seconds", "duration"], seconds);
  let tier = alias(sources, ["resolution"], resolution);
  let ratio = alias(sources, ["ratio", "aspect_ratio", "aspectRatio"], aspectRatio);
  const size = alias(sources, ["size"], function (value) {
    if (typeof value !== "string") throw new Error("size must be a resolution tier or WIDTHxHEIGHT");
    return value.toLowerCase();
  });
  if (size !== undefined) {
    const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(size);
    let sizeTier;
    if (match) {
      const width = Number(match[1]);
      const height = Number(match[2]);
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(function (value) {
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
  const request = { prompt, seconds: duration === undefined ? 5 : duration };
  if (tier !== undefined) request.resolution = tier;
  const audio = alias(sources, ["generate_audio"], function (value) {
    if (typeof value !== "boolean") throw new Error("generate_audio must be a boolean");
    return value;
  });
  // Canvas compatibility only: ChatCut2API ignores this top-level field and
  // the inspected ChatCut submit path has no output-audio toggle. Never invent
  // a native sound field; preserve false and absence without claiming control.
  if (audio !== undefined) request.generate_audio = audio;
  for (const group of MEDIA) {
    const urls = alias(sources, group.names, function (value) {
      if (!Array.isArray(value) || value.length > group.limit || value.some(function (url) { return !httpURL(url); })) throw new Error(group.names[0] + " must be an array of at most " + group.limit + " HTTP(S) URLs without credentials");
      return Array.from(value);
    });
    if (urls && urls.length) request[group.names[0]] = urls;
  }
  const first = alias(sources, ["first_image_url", "first_frame_url", "first_frame", "image"], imageURL);
  const last = alias(sources, ["last_image_url", "last_frame_url", "last_frame", "image_tail", "imageTail"], imageURL);
  if (last && !first) throw new Error("last frame requires a first frame");
  if (first && MEDIA.some(function (group) { return !!request[group.names[0]]; })) throw new Error("first/last frames cannot be combined with reference images, videos or audio");
  const mode = alias(sources, ["functionMode"], function (value) {
    if (value !== "omni_reference" && value !== "first_last_frames") throw new Error("unsupported functionMode");
    return value;
  });
  if (mode !== undefined && mode !== (first ? "first_last_frames" : "omni_reference")) throw new Error("functionMode conflicts with the supplied frames or references");
  if (first) {
    if (ratio !== undefined && ratio !== "adaptive") throw new Error("first/last frames require ratio adaptive");
    request.first_image_url = first;
    if (last) request.last_image_url = last;
  } else if (ratio === "adaptive") throw new Error("ratio adaptive requires a first frame");
  request.aspect_ratio = ratio || (first ? "adaptive" : "16:9");
  return request;
}

function modelRequest(ctx) {
  const name = ctx.upstreamModel || ctx.model;
  const model = has(ALIASES, name) ? ALIASES[name] : name;
  if (!has(MODELS, model)) throw new Error("unsupported ChatCut2API model: " + String(name));
  const request = videoRequest(ctx.requestBody);
  const capability = MODELS[model];
  if (request.seconds > capability.maxSeconds) throw new Error(model + " supports seconds between 4 and " + capability.maxSeconds);
  request.resolution = request.resolution || "720p";
  if (!capability.resolutions.includes(request.resolution)) throw new Error(model + " supports only " + capability.resolutions.join(", "));
  if (model !== "seedance-2-5" && request.audios && !request.images && !request.videos) throw new Error(model + " audio references require an image or video reference");
  return { model, request };
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
  const request = normalized.request;
  const params = { functionMode: request.first_image_url ? "first_last_frames" : "omni_reference" };
  for (const group of MEDIA) {
    if (request[group.names[0]]) params[group.names[2]] = request[group.names[0]];
  }
  if (request.first_image_url) params.image = request.first_image_url;
  if (request.last_image_url) params.imageTail = request.last_image_url;
  // Reference durations are not known here. Do not fabricate zero durations or
  // treat requested output seconds as input seconds. The host measures URLs
  // when a channel's purchase/sales rules charge for input video seconds.
  const body = { model: normalized.model, prompt: request.prompt, seconds: request.seconds, resolution: request.resolution, aspect_ratio: request.aspect_ratio, sync: false, params };
  if (has(request, "generate_audio")) body.generate_audio = request.generate_audio;
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos", method: "POST", headers: { ...authHeaders(ctx), "Content-Type": "application/json" }, body };
}

export function extractUsage(ctx) {
  const request = modelRequest(ctx).request;
  return { seconds: request.seconds, resolution: request.resolution };
}

export function extractUsageOnComplete() {
  // seconds is echoed input; estimated_cost is an account scheduling estimate,
  // and elapsed_seconds is wall time. None is authoritative completion usage.
  return {};
}

export function describeSpec(ctx) {
  const request = modelRequest(ctx).request;
  return {
    spec_version: 3, output_seconds: request.seconds, seconds_kind: "exact", resolution: request.resolution,
    references: { image: (request.images || []).length, video: (request.videos || []).length, audio: (request.audios || []).length, frame: Number(!!request.first_image_url) + Number(!!request.last_image_url) },
    reference_video_urls: request.videos || [],
  };
}

function videoURL(body) {
  return isObject(body) && body.status === "completed" && !body.error && httpURL(body.url) ? body.url : "";
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  // No documented 2xx rejection envelope: missing IDs are ambiguous and must
  // not trigger a second paid submission.
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim()) throw new Error("upstream response is missing a task id");
  const output = { taskId: body.id, taskData: body };
  const result = parseTaskResult({ taskId: body.id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId.trim()) throw new Error("upstream task id is required");
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim() || body.id !== ctx.taskId) return { status: "UNKNOWN", reason: "invalid or mismatched ChatCut2API task id" };
  if (body.status === "failed") return { status: "FAILURE", reason: isObject(body.error) && typeof body.error.message === "string" && body.error.message.trim() ? body.error.message : "video generation failed" };
  if (body.error) return { status: "UNKNOWN", reason: "conflicting ChatCut2API task response" };
  if (body.status === "completed" && videoURL(body)) return { status: "SUCCESS", progress: "100%" };
  const statuses = { queued: "QUEUED", in_progress: "IN_PROGRESS" };
  if (!has(statuses, body.status)) return { status: "UNKNOWN", reason: "unrecognized task status or missing video URL" };
  const result = { status: statuses[body.status] };
  if (typeof body.progress === "number" && Number.isFinite(body.progress) && body.progress >= 0 && body.progress <= 100) result.progress = body.progress + "%";
  return result;
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
  if (/cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/content moderation|content policy violation|内容.{0,12}(违规|敏感|不合规)/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      let input;
      if (body && body.kind === "json") input = body.value;
      else if (body && body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("ChatCut2API requires media URLs; local file uploads are unsupported");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) {
            if (entries.length === 1 && entries[0].startsWith("[")) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be a URL array"); }
            } else input[name] = entries;
          } else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            if (["metadata", "params", "generate_audio"].includes(name)) {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be valid JSON"); }
            } else input[name] = name === "n" && entries[0] === "1" ? 1 : entries[0];
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Final model caps apply after channel mapping, not while filtering the
      // initial shared-model pool, where the upstream identity is not yet known.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody });
      return { kind: "submit", model: input.model, action: requestBody.first_image_url || MEDIA.some(function (group) { return !!requestBody[group.names[0]]; }) ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      // Keep chatcut.raw, account/project/asset ids and estimated credits private.
      return url ? { url } : {};
    },
  },
};
