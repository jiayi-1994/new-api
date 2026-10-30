// Recapo2API app/models.py, main.py and service.py, checked 2026-10-28.
// The gateway forbids unknown fields, so only its own request fields are sent.
const MODELS = {
  seedance2mini: { resolutions: ["720p"], maxSeconds: 15, maxReferences: 9, video: false },
  seedance2fast: { resolutions: ["720p"], maxSeconds: 15, maxReferences: 9, video: false },
  seedance2: { resolutions: ["720p", "1080p"], maxSeconds: 15, maxReferences: 9, video: false },
  "seedance2.5": { resolutions: ["720p", "1080p"], maxSeconds: 30, maxReferences: 30, video: false },
  // Only this doubao-seedance-2.0-fast route accepted video references live;
  // Mini rejected them and the other special routes remain unverified.
  seedance2fastofficial: { resolutions: ["480p", "720p"], maxSeconds: 15, maxReferences: 9, video: true },
};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
const MEDIA = [
  { names: ["images", "reference_images", "referenceImages", "imageUrls", "image_urls"], wire: "reference_images", kind: "image", limit: 30 },
  { names: ["videos", "reference_videos", "referenceVideos", "videoUrls", "video_urls"], wire: "reference_videos", kind: "video", limit: 10 },
  { names: ["audios", "reference_audios", "referenceAudios", "audioUrls", "audio_urls"], wire: "reference_audios", kind: "audio", limit: 10 },
];
const MEDIA_FIELDS = MEDIA.flatMap(function (group) { return group.names; });
const FIRST_FRAME = ["first_image_url", "first_frame_url", "first_frame", "image"];
const LAST_FRAME = ["last_image_url", "last_frame_url", "last_frame", "image_tail", "imageTail"];
const PARAMETERS = ["prompt", "seconds", "duration", "resolution", "size", "ratio", "aspect_ratio", "aspectRatio", "n", "generate_audio"].concat(MEDIA_FIELDS, FIRST_FRAME, LAST_FRAME);
const FIELDS = ["model", "metadata"].concat(PARAMETERS);

export const meta = {
  apiVersion: 1,
  key: "recapo2api",
  name: "Recapo2API",
  version: "1.0.1",
  author: { name: "jiayi-1994" },
  icon: "text:RC",
  description: { en: "Seedance video generation with reference media and first/last frames through Recapo2API", zh: "通过 Recapo2API 使用参考素材和首尾帧生成 Seedance 视频" },
  baseUrl: "http://82.158.230.113:8794",
  models: Object.keys(MODELS),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  // Same keys as chatcut2api, which shares three of these models.
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    resolution: { enum: ["480p", "720p", "1080p"], description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  },
  usageExamples: [{ label: "720p · 4s", facts: { seconds: 4, resolution: "720p" } }],
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

// One normalization chain for decode, billing, scheduling and submit.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const name of Object.keys(input)) {
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  const sources = [input];
  if (has(input, "metadata")) {
    if (!isObject(input.metadata)) throw new Error("metadata must be an object");
    for (const key of Object.keys(input.metadata)) {
      if (!PARAMETERS.includes(key)) throw new Error("unsupported metadata parameter: " + key);
    }
    sources.push(input.metadata);
  }
  const prompt = alias(sources, ["prompt"], function (value) {
    if (typeof value !== "string" || !value.trim()) throw new Error("prompt is required");
    if (Array.from(value.trim()).length > 5000) throw new Error("Recapo prompt must be at most 5000 characters");
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
  // Resolve size here so the billed tier is fixed; size is never forwarded.
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
  const request = { prompt, seconds: duration === undefined ? 4 : duration, resolution: tier || "720p", aspect_ratio: ratio || "16:9" };
  // Recapo2API forwards this to the upstream generate_audio field; absence
  // keeps the gateway default instead of inventing a value.
  const audio = alias(sources, ["generate_audio"], function (value) {
    if (typeof value !== "boolean") throw new Error("generate_audio must be a boolean");
    return value;
  });
  if (audio !== undefined) request.generate_audio = audio;
  for (const group of MEDIA) {
    const urls = alias(sources, group.names, function (value) {
      if (!Array.isArray(value) || value.length > group.limit || value.some(function (url) { return !httpURL(url); })) throw new Error(group.names[0] + " must be an array of at most " + group.limit + " HTTP(S) URLs without credentials");
      return Array.from(value);
    });
    if (urls && urls.length) request[group.wire] = urls;
  }
  const first = alias(sources, FIRST_FRAME, imageURL);
  const last = alias(sources, LAST_FRAME, imageURL);
  if (last && !first) throw new Error("last frame requires a first frame");
  if (first && MEDIA.some(function (group) { return !!request[group.wire]; })) throw new Error("first/last frames cannot be combined with reference images, videos or audio");
  if (first) request.first_frame = first;
  if (last) request.last_frame = last;
  // Match Recapo2API's automatic reference prefix without changing the body.
  // Count Unicode characters, as Python/Pydantic does, before candidate selection.
  let promptLength = Array.from(prompt).length;
  if (!/@(图|图片|image|视频|video|音频|audio)\d+/i.test(prompt)) {
    const tags = [];
    const counts = [
      ["图", (request.reference_images || []).length + Number(!!first) + Number(!!last)],
      ["视频", (request.reference_videos || []).length],
      ["音频", (request.reference_audios || []).length],
    ];
    for (const [label, count] of counts) {
      for (let i = 1; i <= count; i++) tags.push("@" + label + i);
    }
    if (tags.length) promptLength += Array.from("参考素材：" + tags.join(" ") + "\n").length;
  }
  if (promptLength > 5000) throw new Error("Recapo prompt must be at most 5000 characters including automatic reference tags; got " + promptLength);
  return request;
}

function modelRequest(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  if (!has(MODELS, model)) throw new Error("unsupported Recapo2API model: " + String(model));
  const request = videoRequest(ctx.requestBody);
  const capability = MODELS[model];
  if (request.seconds > capability.maxSeconds) throw new Error(model + " supports seconds between 4 and " + capability.maxSeconds);
  if (!capability.resolutions.includes(request.resolution)) throw new Error(model + " supports only " + capability.resolutions.join(", "));
  if (request.reference_videos && !capability.video) throw new Error(model + " does not accept reference videos; use seedance2fastofficial");
  const total = MEDIA.reduce(function (sum, group) { return sum + (request[group.wire] || []).length; }, 0);
  if (total > capability.maxReferences) throw new Error(model + " accepts at most " + capability.maxReferences + " reference media items");
  if (request.reference_audios && !request.reference_images && !request.reference_videos) throw new Error(model + " audio references require an image or video reference");
  return { model, request };
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string" || !credential.trim()) throw new Error("channel API key is required");
  const token = credential.trim().replace(/^Bearer\s+/i, "");
  if (!token || /\s/.test(token)) throw new Error("invalid channel API key");
  return { Authorization: "Bearer " + token };
}

function videoPath(ctx, taskId) {
  if (typeof taskId !== "string" || !taskId.trim()) throw new Error("upstream task id is required");
  return ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos/" + encodeURIComponent(taskId);
}

export function buildSubmitRequest(ctx) {
  const normalized = modelRequest(ctx);
  const headers = { ...authHeaders(ctx), "Content-Type": "application/json" };
  // The same public task always sends the same body, so a host retry that
  // reaches Recapo2API again returns the existing paid task instead of a new one.
  if (typeof ctx.publicTaskId === "string" && ctx.publicTaskId) headers["Idempotency-Key"] = ctx.publicTaskId;
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos", method: "POST", headers, body: { model: normalized.model, ...normalized.request, sync: false } };
}

export function extractUsage(ctx) {
  const request = modelRequest(ctx).request;
  return { seconds: request.seconds, resolution: request.resolution };
}

export function extractUsageOnComplete() {
  // seconds is echoed input and points_* are Recapo account credits; none is
  // authoritative completion usage.
  return {};
}

export function describeSpec(ctx) {
  const request = modelRequest(ctx).request;
  return {
    spec_version: 3, output_seconds: request.seconds, seconds_kind: "exact", resolution: request.resolution,
    references: { image: (request.reference_images || []).length, video: (request.reference_videos || []).length, audio: (request.reference_audios || []).length, frame: Number(!!request.first_frame) + Number(!!request.last_frame) },
    reference_video_urls: request.reference_videos || [],
  };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  // Missing IDs are ambiguous and must not trigger a second paid submission.
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim()) throw new Error("upstream response is missing a task id");
  const output = { taskId: body.id, taskData: body };
  const result = parseTaskResult({ taskId: body.id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  return { url: videoPath(ctx, ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim() || body.id !== ctx.taskId) return { status: "UNKNOWN", reason: "invalid or mismatched Recapo2API task id" };
  if (body.status === "failed") return { status: "FAILURE", reason: isObject(body.error) && typeof body.error.message === "string" && body.error.message.trim() ? body.error.message : "video generation failed" };
  if (body.error) return { status: "UNKNOWN", reason: "conflicting Recapo2API task response" };
  // submission_unknown is reported as in_progress, so polling continues
  // until Recapo2API reconciles the paid submission.
  const statuses = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS" };
  if (!has(statuses, body.status)) return { status: "UNKNOWN", reason: "unrecognized task status" };
  return { status: statuses[body.status], progress: body.status === "completed" ? "100%" : "0%" };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && isObject(task.data) && task.data.status === "completed" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video" || !isObject(ctx.data) || ctx.data.status !== "completed") throw new Error("artifact_not_found");
  // Recapo2API /content needs the channel key, then streams or 302s to a
  // signed OSS URL that the host hands to the client without credentials.
  // Free Recapo accounts are answered with 402 subscription_required.
  return { url: videoPath(ctx, ctx.upstreamTaskId || ctx.taskId) + "/content", method: ctx.clientRequest.method, headers: authHeaders(ctx) };
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
        if ((body.files || []).length) throw new Error("Recapo2API requires media URLs; local file uploads are unsupported");
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
            if (["metadata", "generate_audio"].includes(name)) {
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
      return { kind: "submit", model: input.model, action: requestBody.first_frame || MEDIA.some(function (group) { return !!requestBody[group.wire]; }) ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function () {
      // Recapo2API's url is its own key-protected endpoint; the host serves
      // /v1/videos/:id/content. Account, project and credit fields stay private.
      return {};
    },
  },
};
