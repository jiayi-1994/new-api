// Contract: pixverse2api/app/{models,main,service}.py, checked 2026-10-29.
// Automatic neutral references remain controlled by pixverse2api's settings.
// The spec counts client media only; procurement must include any service-side
// reference cost. Automatic output duration is excluded: seconds in a result
// echoes the request, and cost_credits is not a measured video duration.
const MODELS = {
  "seedance-2.0-standard": { maxSeconds: 15, resolutions: ["480p", "720p", "1080p", "2160p"], limits: [9, 3, 3] },
  "seedance-2.0-fast": { maxSeconds: 15, resolutions: ["480p", "720p"], limits: [9, 3, 3] },
  "seedance-2.0-mini": { maxSeconds: 15, resolutions: ["480p", "720p"], limits: [9, 3, 3] },
  "seedance-2.5": { maxSeconds: 30, resolutions: ["480p", "720p", "1080p"], limits: [30, 10, 10] },
};
const ALIASES = {
  "seedance-2.0": "seedance-2.0-standard",
  seedance2: "seedance-2.0-standard",
  seedance2fast: "seedance-2.0-fast",
  seedance2mini: "seedance-2.0-mini",
  "seedance-2-5": "seedance-2.5",
  "seedance2.5": "seedance-2.5",
};
const RATIOS = ["16:9", "4:3", "1:1", "3:4", "9:16", "21:9"];
const MEDIA = [
  { names: ["images", "reference_images"], field: "reference_images", max: 30 },
  { names: ["videos", "reference_videos"], field: "reference_videos", max: 10 },
  { names: ["audios", "reference_audios"], field: "reference_audios", max: 10 },
];
const MEDIA_FIELDS = MEDIA.flatMap(function (group) {
  return group.names;
});
const FIRST_FRAME = ["first_image_url", "first_frame_url", "first_frame", "image"];
const LAST_FRAME = ["last_image_url", "last_frame_url", "last_frame", "image_tail"];
const PARAMETERS = [
  "prompt",
  "seconds",
  "duration",
  "resolution",
  "quality",
  "size",
  "ratio",
  "aspect_ratio",
  "n",
  "generate_audio",
  "relax_mode",
  "seed",
  "task_type",
].concat(MEDIA_FIELDS, FIRST_FRAME, LAST_FRAME);
const FIELDS = ["model", "metadata"].concat(PARAMETERS);

export const meta = {
  apiVersion: 1,
  key: "pixverse2api",
  name: "PixVerse2API",
  version: "1.0.0",
  author: { name: "jiayi-1994" },
  icon: "text:PV",
  description: { en: "Seedance video generation through PixVerse2API", zh: "通过 PixVerse2API 生成 Seedance 视频" },
  models: Object.keys(MODELS).concat(Object.keys(ALIASES)),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    resolution: { enum: ["480p", "720p", "1080p", "2160p"], description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  },
  usageExamples: [{ label: "720p · 5s", facts: { seconds: 5, resolution: "720p" } }],
};

function has(value, key) {
  return Object.prototype.hasOwnProperty.call(value, key);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function mediaURL(value) {
  // The service resolves DNS and enforces public destinations. Local paths,
  // data URLs and account-scoped file IDs cannot be routed across channels.
  if (
    typeof value !== "string" ||
    value.length > 4096 ||
    /[\s\\]/.test(value) ||
    Array.from(value).some(function (character) {
      return character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127;
    }) ||
    !/^https:\/\/(?:\[[0-9a-f:]+\]|[a-z0-9.-]+)(?::443)?(?:[/?#].*)?$/i.test(value)
  ) {
    throw new Error("media must be an HTTPS URL without credentials on port 443");
  }
  return value;
}

function outputSeconds(value) {
  // The provider's 30-second cap is stricter than MaxTaskDurationSeconds.
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+$/.test(String(value))) {
    throw new Error("seconds must be an integer between 4 and 30; automatic duration is unsupported");
  }
  const number = Number(value);
  if (!Number.isInteger(number) || number < 4 || number > 30) throw new Error("seconds must be an integer between 4 and 30");
  return number;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be 480p, 720p, 1080p or 2160p");
  const tier = value.toLowerCase() === "4k" ? "2160p" : value.toLowerCase();
  if (!meta.usageSchema.resolution.enum.includes(tier)) throw new Error("resolution must be 480p, 720p, 1080p or 2160p");
  return tier;
}

// Equivalent mirror fields count once; repeated array entries remain intact.
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

// All entry surfaces and driver hooks share this immutable normalization.
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
  const prompt = alias(sources, ["prompt"], function (value) {
    if (typeof value !== "string" || !value.trim() || Array.from(value).length > 10000) throw new Error("prompt must contain 1 to 10000 characters");
    return value;
  });
  if (prompt === undefined) throw new Error("prompt is required");
  alias(sources, ["n"], function (value) {
    if (value !== 1) throw new Error("n must be 1");
    return value;
  });
  const duration = alias(sources, ["seconds", "duration"], outputSeconds);
  let tier = alias(sources, ["resolution", "quality"], resolution);
  let ratio = alias(sources, ["ratio", "aspect_ratio"], function (value) {
    if (value !== "auto" && !RATIOS.includes(value)) throw new Error("unsupported aspect ratio");
    return value;
  });
  const size = alias(sources, ["size"], function (value) {
    if (typeof value !== "string") throw new Error("size must be a resolution tier or WIDTHxHEIGHT");
    return value.toLowerCase();
  });
  if (size !== undefined) {
    const dimensions = /^([1-9]\d{1,4})x([1-9]\d{1,4})$/.exec(size);
    let sizeTier;
    if (dimensions) {
      const width = Number(dimensions[1]),
        height = Number(dimensions[2]);
      // Preserve exact tiers for host sales; do not silently bucket arbitrary
      // dimensions into a different output resolution as the service can do.
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(function (candidate) {
        const parts = candidate.split(":");
        return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.03;
      });
      if (!sizeRatio) throw new Error("size has an unsupported aspect ratio");
      if (ratio !== undefined && ratio !== sizeRatio) throw new Error("size conflicts with ratio");
      ratio = sizeRatio;
    } else sizeTier = resolution(size);
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    tier = sizeTier;
  }
  const request = { prompt, seconds: duration === undefined ? 5 : duration, resolution: tier || "720p", aspect_ratio: ratio || "16:9" };
  for (const name of ["generate_audio", "relax_mode"]) {
    const value = alias(sources, [name], function (entry) {
      if (typeof entry !== "boolean") throw new Error(name + " must be a boolean");
      return entry;
    });
    if (value !== undefined) request[name] = value;
  }
  const seed = alias(sources, ["seed"], function (value) {
    if (typeof value !== "number" || !Number.isInteger(value) || value < 0 || value > 2147483647)
      throw new Error("seed must be an integer between 0 and 2147483647");
    return value;
  });
  if (seed !== undefined) request.seed = seed;
  const taskType = alias(sources, ["task_type"], function (value) {
    if (!["auto", "reference", "edit", "extend"].includes(value)) throw new Error("unsupported task_type");
    return value;
  });
  if (taskType !== undefined) request.task_type = taskType;
  for (const group of MEDIA) {
    const urls = alias(sources, group.names, function (value) {
      if (!Array.isArray(value) || value.length > group.max) throw new Error(group.names[0] + " must be an array of at most " + group.max + " HTTPS URLs");
      return Array.from(value, mediaURL);
    });
    if (urls && urls.length) request[group.field] = urls;
  }
  const first = alias(sources, FIRST_FRAME, mediaURL),
    last = alias(sources, LAST_FRAME, mediaURL);
  if (last && !first) throw new Error("last frame requires a first frame");
  if (
    first &&
    MEDIA.some(function (group) {
      return !!request[group.field];
    })
  )
    throw new Error("first/last frames cannot be combined with reference media");
  if (first) request.first_frame = first;
  if (last) request.last_frame = last;
  if (first && ratio === "auto") throw new Error("first/last frames require a fixed aspect ratio");
  if (first && taskType !== undefined) throw new Error("task_type is only supported with reference media");
  if ((taskType === "edit" || taskType === "extend") && !request.reference_videos) throw new Error("edit/extend requires reference videos");
  return request;
}

function modelRequest(ctx) {
  const upstream = ctx.upstreamModel || ctx.model;
  const model = has(ALIASES, upstream) ? ALIASES[upstream] : upstream;
  if (!has(MODELS, model)) throw new Error("unsupported PixVerse2API model: " + String(upstream));
  const request = videoRequest(ctx.requestBody),
    capability = MODELS[model];
  if (request.seconds > capability.maxSeconds) throw new Error(model + " supports seconds between 4 and " + capability.maxSeconds);
  if (!capability.resolutions.includes(request.resolution)) throw new Error(model + " supports only " + capability.resolutions.join(", "));
  for (let i = 0; i < MEDIA.length; i++) {
    if ((request[MEDIA[i].field] || []).length > capability.limits[i])
      throw new Error(model + " accepts at most " + capability.limits[i] + " " + MEDIA[i].field);
  }
  if (model === "seedance-2.5") {
    if (has(request, "generate_audio")) throw new Error("Seedance 2.5 does not support generate_audio; omit the field");
  } else {
    if (request.aspect_ratio === "auto") throw new Error("automatic aspect ratio requires Seedance 2.5");
    if (has(request, "task_type")) throw new Error("task_type requires Seedance 2.5");
  }
  // Audio-only and reference-mode preparation remain subject to the service's
  // automatic-reference setting, as requested by the gateway operator.
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
  if (typeof ctx.publicTaskId === "string" && ctx.publicTaskId) headers["Idempotency-Key"] = ctx.publicTaskId;
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos",
    method: "POST",
    headers,
    body: { model: normalized.model, ...normalized.request, sync: false },
  };
}

export function extractUsage(ctx) {
  const request = modelRequest(ctx).request;
  return { seconds: request.seconds, resolution: request.resolution };
}

export function extractUsageOnComplete() {
  // elapsed_seconds is wall time, seconds is echoed input, cost_credits is an
  // upstream account debit. None is authoritative completion duration.
  return {};
}

export function describeSpec(ctx) {
  const request = modelRequest(ctx).request;
  return {
    spec_version: 3,
    output_seconds: request.seconds,
    seconds_kind: "exact",
    resolution: request.resolution,
    references: {
      video: (request.reference_videos || []).length,
      image: (request.reference_images || []).length,
      audio: (request.reference_audios || []).length,
      frame: Number(!!request.first_frame) + Number(!!request.last_frame),
    },
    reference_video_urls: request.reference_videos || [],
  };
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body) || typeof body.id !== "string" || !body.id.trim()) throw new Error("upstream response is missing a task id");
  const output = { taskId: body.id, taskData: body };
  const result = parseTaskResult({ taskId: body.id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  return { url: videoPath(ctx, ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

function videoURL(data) {
  if (!isObject(data) || data.status !== "completed") return "";
  const url = data.video_url || data.url;
  if (
    typeof url !== "string" ||
    url.length > 8192 ||
    /[\s\\]/.test(url) ||
    Array.from(url).some(function (character) {
      return character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127;
    }) ||
    !/^https:\/\/[^/?#@]+(?:[/?][^#]*)?$/.test(url)
  )
    return "";
  return url;
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || body.id !== ctx.taskId || typeof body.id !== "string" || !body.id.trim())
    return { status: "UNKNOWN", reason: "invalid or mismatched PixVerse2API task id" };
  if (body.status === "failed")
    return {
      status: "FAILURE",
      reason: isObject(body.error) && typeof body.error.message === "string" && body.error.message.trim() ? body.error.message : "video generation failed",
    };
  // submission_unknown / needs_attention are public in_progress tasks. Their
  // error is diagnostic, not a terminal failure: keep polling without refund.
  const statuses = { queued: "QUEUED", in_progress: "IN_PROGRESS", completed: "SUCCESS" };
  if (!has(statuses, body.status)) return { status: "UNKNOWN", reason: "unrecognized task status" };
  if (body.status === "completed" && (body.error || !videoURL(body))) return { status: "UNKNOWN", reason: "completed task is missing a valid video result" };
  return { status: statuses[body.status], progress: body.status === "completed" ? "100%" : "0%" };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && videoURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const url = videoURL(ctx.data);
  if (ctx.artifactKey !== "video" || !url) throw new Error("artifact_not_found");
  // The service's /content endpoint supports GET only. Its authenticated poll
  // already returns this signed URL, which the host can serve for GET and HEAD
  // without forwarding either channel or client credentials to the media host.
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
        if ((body.files || []).length) throw new Error("PixVerse2API requires HTTPS media URLs; local file uploads are unsupported");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) {
            input[name] = entries.length === 1 && entries[0].startsWith("[") ? JSON.parse(entries[0]) : entries;
          } else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            if (["metadata", "generate_audio", "relax_mode", "n", "seed"].includes(name)) input[name] = JSON.parse(entries[0]);
            else input[name] = entries[0];
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Initial shared-model filtering precedes channel mapping. Apply model
      // caps once the host supplies the selected upstream identity.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody });
      return {
        kind: "submit",
        model: input.model,
        action:
          requestBody.first_frame ||
          MEDIA.some(function (group) {
            return !!requestBody[group.field];
          })
            ? "reference_to_video"
            : "text_to_video",
        requestBody,
      };
    },
    render: function () {
      // The host exposes /v1/videos/:id/content. Keep upstream ids, account
      // credits, traces and diagnostic errors out of the public response.
      return {};
    },
  },
};
