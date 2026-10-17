// Vobilemax's asynchronous /v1/videos API, task-plugin-api.md (2026-10-12).
// Keep a relay-specific model identity: the official Doubao driver has a
// different wire protocol, billing unit and scheduling capability.
const MAX_SECONDS = 3600; // relay/common.MaxTaskDurationSeconds; not a vendor capability claim
const RESOLUTIONS = ["480p", "720p", "1080p", "4k"];
// Model capabilities come from the supplied Vobilemax catalog (2026-10-12).
// Seedance 2.0's 1080p/4k tiers are explicitly enabled at the user's request;
// its existing facade duration range is retained, not inferred from 2.5.
const MODELS = [
  { model: "vobilemax-seedance-2.0", upstream: "doubao-seedance-2-0-260128", resolutions: RESOLUTIONS },
  { model: "vobilemax-seedance-2.5", upstream: "doubao-seedance-2-5-260628", resolutions: ["480p", "720p", "1080p"], adaptiveFrames: true },
  { model: "vobilemax-kling-v3", upstream: "kling-v3", resolutions: ["720p", "1080p", "4k"], framesOnly: true },
];
const VIDEO_USAGE_SCHEMA = {
  seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
  resolution: { enum: RESOLUTIONS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  video_input: { type: "boolean", description: { en: "Reference video present", zh: "存在参考视频" } },
};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "adaptive"];
const MEDIA = [
  ["images", "imageUrls", "image_urls"],
  ["videos", "videoUrls", "video_urls"],
  ["audios", "audioUrls", "audio_urls"],
];
const FIELDS = [
  "model",
  "prompt",
  "seconds",
  "duration",
  "size",
  "resolution",
  "ratio",
  "aspect_ratio",
  "generate_audio",
  "n",
  "options",
  "mode",
  "sync",
  "first_image_url",
  "last_image_url",
].concat(...MEDIA);

export const meta = {
  apiVersion: 1,
  key: "vobilemax",
  name: "Vobilemax",
  version: "1.2.1",
  author: { name: "jiayi-1994" },
  icon: "text:V",
  description: { en: "Video generation through the Vobilemax API", zh: "通过 Vobilemax 接口生成视频" },
  baseUrl: "http://64.83.42.250:8787",
  models: MODELS.map(function (model) {
    return model.model;
  }),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  // These are customer-facing usage facts, not vendor CREDIT or USD prices.
  // Unified video sales use the host's frozen sale instead. Vendor quote and
  // final creditCost remain in taskData; neither is a reliable pre-submit fact.
  usageSchema: VIDEO_USAGE_SCHEMA,
  usageProfiles: MODELS.map(function (model) {
    return {
      models: [model.model],
      schema: Object.assign({}, VIDEO_USAGE_SCHEMA, {
        resolution: { enum: model.resolutions, description: VIDEO_USAGE_SCHEMA.resolution.description },
      }),
    };
  }),
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function httpURL(value) {
  return typeof value === "string" && value.length <= 8192 && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value);
}

function seconds(value, field) {
  const text = typeof value === "number" || typeof value === "string" ? String(value).trim() : "";
  if (!/^[1-9]\d{0,3}$/.test(text) || Number(text) > MAX_SECONDS) throw new Error(field + " must be between 1 and 3600 integer seconds");
  return Number(text);
}

function resolution(value, field) {
  let text = typeof value === "string" ? value.trim().toLowerCase() : "";
  if (text === "2160p") text = "4k";
  if (!RESOLUTIONS.includes(text)) throw new Error(field + " must be 480p, 720p, 1080p or 4k");
  return text;
}

function ratio(value, field) {
  const text = typeof value === "string" ? value.trim() : "";
  if (!RATIOS.includes(text)) throw new Error(field + " must be one of " + RATIOS.join(", "));
  return text;
}

// Equal aliases are accepted; conflicting aliases fail before billing. Never
// silently price one duration/resolution and submit another via options.
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

function videoRequest(input, model) {
  if (!isObject(input)) throw new Error("video request must be a JSON object");
  for (const name of Object.keys(input)) {
    if (name === "ref_video_seconds" || name === "refVideoSecondsHint")
      throw new Error("client reference duration hints are not accepted; omit them and let the upstream determine reference duration");
    if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
  }
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt must be a non-empty string");
  if (has(input, "mode") && !["allReference", "image2video", "firstLastFrame"].includes(input.mode))
    throw new Error("mode must be allReference, image2video or firstLastFrame");
  if (has(input, "sync") && input.sync !== false) throw new Error("sync must be false; tasks are polled asynchronously");
  if (has(input, "n") && input.n !== 1 && input.n !== "1") throw new Error("n must be 1");
  const options = has(input, "options") ? input.options : {};
  if (!isObject(options)) throw new Error("options must be an object");
  for (const name of Object.keys(options)) {
    if (!["ratio", "aspectRatio", "resolution", "quality", "duration", "durationSeconds", "count"].includes(name))
      throw new Error("unsupported options parameter: " + name);
  }
  if (has(options, "count") && options.count !== 1 && options.count !== "1") throw new Error("options.count must be 1");
  const duration = aliased([input, options], ["seconds", "duration", "durationSeconds"], seconds);
  let tier = aliased([input, options], ["resolution", "quality"], resolution);
  let aspect = aliased([input, options], ["ratio", "aspect_ratio", "aspectRatio"], ratio);
  if (has(input, "size")) {
    const text = typeof input.size === "string" ? input.size.trim().toLowerCase() : "";
    let sizeTier;
    let sizeRatio;
    if (RESOLUTIONS.includes(text) || text === "2160p") sizeTier = resolution(text, "size");
    else {
      const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(text);
      if (!match) throw new Error("size must be a supported tier or WIDTHxHEIGHT");
      const width = Number(match[1]),
        height = Number(match[2]);
      sizeTier = resolution(Math.min(width, height) + "p", "size");
      // The gateway treats 2560x1080 as 21:9, despite the rounded pixel ratio.
      sizeRatio =
        width === 2560 && height === 1080
          ? "21:9"
          : RATIOS.find(function (item) {
              const parts = item.split(":");
              return width * Number(parts[1]) === height * Number(parts[0]);
            });
      if (!sizeRatio) throw new Error("size has an unsupported aspect ratio");
    }
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    if (aspect !== undefined && sizeRatio !== undefined && aspect !== sizeRatio) throw new Error("size conflicts with ratio");
    tier = sizeTier;
    if (sizeRatio !== undefined) aspect = sizeRatio;
  }
  const output = { prompt: input.prompt, seconds: duration === undefined ? 15 : duration, resolution: tier || "720p" };
  if (has(input, "generate_audio")) {
    if (typeof input.generate_audio !== "boolean") throw new Error("generate_audio must be a boolean");
    output.generate_audio = input.generate_audio;
  }
  for (const names of MEDIA) {
    let urls;
    for (const name of names) {
      if (!has(input, name)) continue;
      if (!Array.isArray(input[name])) throw new Error(name + " must be an array of public http(s) URLs");
      const current = input[name].map(function (value) {
        const url = typeof value === "string" ? value.trim() : "";
        if (!httpURL(url)) throw new Error(name + " entries must be public http(s) URLs without credentials");
        return url;
      });
      if (urls !== undefined && JSON.stringify(urls) !== JSON.stringify(current)) throw new Error("conflicting values for " + names.join("/"));
      urls = current;
    }
    // Preserve duplicates and order: each submitted reference is independently billable.
    output[names[0]] = urls || [];
  }
  const frames = [];
  for (const name of ["first_image_url", "last_image_url"]) {
    if (!has(input, name)) continue;
    const url = typeof input[name] === "string" ? input[name].trim() : "";
    if (!httpURL(url)) throw new Error(name + " must be a public http(s) URL without credentials");
    frames.push(url);
  }
  if (has(input, "last_image_url") && !has(input, "first_image_url")) throw new Error("last_image_url requires first_image_url");
  if (frames.length) {
    if (output.images.length) throw new Error("first/last frame URLs conflict with images/imageUrls");
    if (has(input, "mode") && input.mode === "allReference") throw new Error("first/last frame URLs require image2video or firstLastFrame mode");
    output.images = frames;
  }
  // Keep an omitted mode unresolved for a public alias until its channel model
  // is known. Kling's ordered imageUrls are conditioning frames, not references.
  let mode = input.mode;
  if (!mode && (frames.length || (model && model.framesOnly))) mode = output.images.length > 1 ? "firstLastFrame" : "image2video";
  if (mode) output.mode = mode;
  const frameMode = mode === "image2video" || mode === "firstLastFrame";
  if (frameMode) {
    if (output.images.length < 1 || output.images.length > (mode === "image2video" ? 1 : 2))
      throw new Error(mode + " requires " + (mode === "image2video" ? "one image" : "one or two images in first/last frame order"));
    if (output.videos.length || output.audios.length) throw new Error("frame modes do not support reference videos or audio");
  }
  if (model) {
    if (!model.resolutions.includes(output.resolution)) throw new Error(model.model + " resolution must be " + model.resolutions.join(", "));
    if (model.framesOnly) {
      if (!frameMode) throw new Error("Kling requires image2video or firstLastFrame mode");
      if (![3, 5, 10, 15].includes(output.seconds)) throw new Error("Kling seconds must be 3, 5, 10 or 15");
    }
    if (model.upstream === "doubao-seedance-2-5-260628") {
      if (output.seconds < 4 || output.seconds > 30) throw new Error("Seedance 2.5 seconds must be between 4 and 30");
      if (output.images.length > 30 || output.videos.length > 10 || output.audios.length > 10)
        throw new Error("Seedance 2.5 supports at most 30 images, 10 videos and 10 audio references");
    }
  }
  // Seedance 2.5 locks frame modes to the first image's aspect ratio. Preserve
  // an omitted ratio during alias decoding until the mapped model is known;
  // otherwise a synthetic 16:9 default would look like a conflicting request.
  if (frameMode && model && model.adaptiveFrames) {
    if (aspect !== undefined && aspect !== "adaptive")
      throw new Error("Seedance 2.5 frame modes require ratio adaptive; omit ratio and pixel size to follow the first frame");
    output.ratio = "adaptive";
  } else if (aspect !== undefined || model || !frameMode) {
    output.ratio = aspect || "16:9";
  }
  return output;
}

function videoModel(name, required) {
  const model = MODELS.find(function (item) {
    return name === item.model || name === item.upstream;
  });
  if (!model && required) throw new Error("unsupported Vobilemax upstream model: " + name);
  return model;
}

function baseURL(ctx) {
  if (typeof ctx.baseUrl !== "string" || !/^https?:\/\/[^\s/?#@\\]+(?:\/[^\s?#\\]*)?$/i.test(ctx.baseUrl))
    throw new Error("channel Base URL must be an http(s) address without credentials, query or fragment");
  return ctx.baseUrl.replace(/\/+$/, "");
}

function authHeaders(ctx) {
  const value = ctx.authHeader || ctx.apiKey;
  const token =
    typeof value === "string"
      ? value
          .trim()
          .replace(/^Bearer\s+/i, "")
          .trim()
      : "";
  if (!token || /\s/.test(token)) throw new Error("channel API key is required and must not contain whitespace");
  return { Authorization: "Bearer " + token };
}

export function buildSubmitRequest(ctx) {
  const model = videoModel(ctx.upstreamModel || ctx.model, true);
  const params = videoRequest(ctx.requestBody, model);
  const request = {
    url: baseURL(ctx) + "/v1/videos",
    method: "POST",
    headers: Object.assign(authHeaders(ctx), { "Content-Type": "application/json; charset=utf-8" }),
    body: {
      model: model.upstream,
      prompt: params.prompt,
      seconds: params.seconds,
      sync: false,
      mode: params.mode || "allReference",
      imageUrls: params.images,
      videoUrls: params.videos,
      audioUrls: params.audios,
      // Set both native spellings explicitly: the facade otherwise fills the
      // secondary spellings from environment defaults, which may disagree.
      options: {
        duration: params.seconds,
        durationSeconds: params.seconds,
        count: 1,
        resolution: params.resolution,
        quality: params.resolution,
        ratio: params.ratio,
        aspectRatio: params.ratio,
      },
      // No ref_video_seconds: it is not the output duration, nor a trusted client fact.
    },
  };
  // Preserve the canvas flag, including false, without inventing a default.
  // The current Vobilemax facade accepts this extra field but does not map it
  // to a native audio control, so forwarding it does not guarantee audio behavior.
  if (has(params, "generate_audio")) request.body.generate_audio = params.generate_audio;
  return request;
}

export function extractUsage(ctx) {
  const params = videoRequest(ctx.requestBody, videoModel(ctx.upstreamModel || ctx.model, true));
  return { seconds: params.seconds, resolution: params.resolution, video_input: params.videos.length > 0 };
}

export function describeSpec(ctx) {
  const params = videoRequest(ctx.requestBody, videoModel(ctx.upstreamModel || ctx.model, true));
  if (params.mode === "image2video" || params.mode === "firstLastFrame") {
    return {
      spec_version: 3,
      output_seconds: params.seconds,
      seconds_kind: "exact",
      resolution: params.resolution,
      references: { image: 0, video: 0, audio: 0, frame: params.images.length },
    };
  }
  return {
    spec_version: 2,
    output_seconds: params.seconds,
    seconds_kind: "exact",
    resolution: params.resolution,
    references: { image: params.images.length, video: params.videos.length, audio: params.audios.length },
    reference_video_urls: params.videos,
  };
}

// Vobilemax returns seconds:null and creditCost:0 at creation. Neither may
// overwrite reserved output seconds; quote CREDIT is not a USD charge.
export function extractUsageOnComplete() {
  return {};
}

function failureReason(body) {
  const error = isObject(body) && body.error;
  if (isObject(error)) {
    const code = typeof error.code === "string" ? error.code : "";
    const message = typeof error.message === "string" ? error.message : "";
    if (code || message) return (code ? code + ": " : "") + message;
  }
  return typeof error === "string" && error ? error : "video generation failed";
}

export function parseTaskResult(_ctx, body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "invalid upstream task response" };
  const statuses = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS", failed: "FAILURE" };
  const status = has(statuses, body.status) ? statuses[body.status] : undefined;
  if (!status) return { status: "UNKNOWN", reason: "unrecognized upstream task status" };
  if (status === "SUCCESS" && !httpURL(body.url)) return { status: "UNKNOWN", reason: "completed task has no downloadable video URL" };
  const result = { status };
  if (status === "SUCCESS") result.progress = "100%";
  else if (typeof body.progress === "number" && Number.isFinite(body.progress) && body.progress >= 0 && body.progress <= 100)
    result.progress = body.progress + "%";
  if (status === "FAILURE") result.reason = failureReason(body);
  return result;
}

export function parseSubmitResponse(ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("upstream create response must be a JSON object");
  if (body.id != null && (typeof body.id !== "string" || !body.id.trim())) throw new Error("upstream create response has an invalid task id");
  if (!body.id) {
    if ((isObject(body.error) && (body.error.code || body.error.message)) || (typeof body.error === "string" && body.error))
      return { rejected: { reason: failureReason(body) } };
    throw new Error("upstream create response has no task id");
  }
  const output = { taskId: body.id, taskData: body };
  const result = parseTaskResult(ctx, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId.trim()) throw new Error("upstream task id is required");
  return { url: baseURL(ctx) + "/v1/videos/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/timeout|timed out|unavailable|internal|quota|balance|no_capacity|rate limit|authentication|api_key|余额|额度|超时|并发|限流/.test(text))
    return "upstream";
  if (/^(cancelled|canceled|video_generation_cancelled):|cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (
    /^(invalid_prompt|invalid_image|invalid_video|invalid_audio|invalid_url|content_policy_violation|content_moderation|video_request_rejected):|content policy violation|moderation (rejected|blocked)|审核(未通过|不通过|拒绝)|未通过(内容)?审核|内容.{0,12}(违规|敏感|不合规)|prompt 不能为空/.test(
      text
    )
  )
    return "user";
  return "upstream";
}

export function listArtifacts(task) {
  if (task.status !== "SUCCESS" || !isObject(task.data) || !httpURL(task.data.url)) return [];
  return [{ key: "video", type: "video", mimeType: "video/mp4" }];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  if (!isObject(ctx.data) || !httpURL(ctx.data.url)) throw new Error("completed task has no downloadable video URL");
  // Signed OSS URLs are fetched through the host SSRF guard, without channel
  // or client credentials. There is no documented upstream /content endpoint.
  return { url: ctx.data.url, method: ctx.clientRequest && ctx.clientRequest.method === "HEAD" ? "HEAD" : "GET", credentialless: true };
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("Vobilemax accepts JSON with public reference URLs; file uploads are not supported");
      const input = ctx.body.value;
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      const model = input.model.trim();
      if (ctx.model && ctx.model !== model) throw new Error("model does not match the selected channel model");
      // The host may decode a public alias before its channel mapping is known.
      const requestBody = videoRequest(input, videoModel(ctx.upstreamModel || model, false));
      const referenced = requestBody.images.length + requestBody.videos.length + requestBody.audios.length > 0;
      return { kind: "submit", model, action: referenced ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      if (!isObject(task.data)) return {};
      const data = task.data;
      const output = {};
      if (typeof data.size === "string") output.size = data.size;
      if (task.status === "SUCCESS") {
        if (httpURL(data.url)) output.url = data.url;
        if (httpURL(data.thumbnail_url)) output.thumbnail_url = data.thumbnail_url;
      }
      if (task.status === "FAILURE") output.error = { message: failureReason(data) };
      // Do not expose the upstream account, balance, quote or private task id.
      return output;
    },
  },
};
