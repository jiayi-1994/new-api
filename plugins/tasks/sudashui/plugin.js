// Task Plugin API v1 for https://api-docs.sudashuiapi.com/ (2026-10-07).
// Capabilities from the authenticated /api/pricing snapshot in model-catalog.json.
// Exclude image models and the three video entries without capability descriptions.
// Unspecified bf-sdas-2.0 durations use the API's general 4–15 second range.
// Resolution follows the model name: gf3 mini's 480p tag contradicts its 720p name.
// The vendor does not define 2k in pixels; it cannot join unified video sales yet.
const MODELS = {
  "bf-sdas-2.0": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"second"},
  "bf-sdas-2.0-fast": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"second"},
  "bf-sdas-2.0-fast-real-priority": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"second"},
  "bf-sdas-2.0-real-priority": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"second"},
  "bf-sdas-2.5-480p": {"resolution":"480p","min":4,"max":30,"media":[30,10,10],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "bf-sdas-2.5-720p": {"resolution":"720p","min":4,"max":30,"media":[30,10,10],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "ld-sdas-2-cvk": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","promptMax":15000},
  "ld-sdas-cvk-pro-933-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"],"promptMax":15000},
  "sdas-gf-seedance-2.0-1080p": {"resolution":"1080p","min":4,"max":15,"media":[9,0,3],"unit":"second"},
  "sdas-gf-seedance-2.0-2k": {"resolution":"2k","min":4,"max":15,"media":[9,0,3],"unit":"second","schedulable":false},
  "sdas-gf-seedance-2.0-4k": {"resolution":"4k","min":4,"max":15,"media":[9,0,3],"unit":"second"},
  "sdas-gf-seedance-2.0-720p": {"resolution":"720p","min":4,"max":15,"media":[9,0,3],"unit":"second"},
  "sdas-gf-seedance-2.0-fast-720p": {"resolution":"720p","min":4,"max":15,"media":[9,0,3],"unit":"second"},
  "sdas-gf3-seedance-2.0-1080p": {"resolution":"1080p","min":4,"max":15,"media":[9,0,3],"unit":"second"},
  "sdas-gf3-seedance-2.0-720p": {"resolution":"720p","min":4,"max":15,"media":[9,0,3],"unit":"second"},
  "sdas-gf3-seedance-2.0-fast-720p": {"resolution":"720p","min":4,"max":15,"media":[9,0,3],"unit":"second"},
  "sdas-gf3-seedance-2.0-mini-720p": {"resolution":"720p","min":4,"max":15,"media":[9,0,3],"unit":"second"},
  "sdas-gf8-seedance-2-480p": {"resolution":"480p","min":4,"max":15,"media":[9,0,3],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-gf8-seedance-2-5-1080p": {"resolution":"1080p","min":4,"max":30,"media":[30,0,10],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-gf8-seedance-2-5-480p": {"resolution":"480p","min":4,"max":30,"media":[30,0,10],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-gf8-seedance-2-5-720p": {"resolution":"720p","min":4,"max":30,"media":[30,0,10],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-gf8-seedance-2-fast-480p": {"resolution":"480p","min":4,"max":15,"media":[9,0,3],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-gf8-seedance-2-mini-480p": {"resolution":"480p","min":4,"max":15,"media":[9,0,3],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-hn-sd2.0-fast-720p": {"resolution":"720p","min":5,"max":15,"media":[4,3,1],"unit":"count","durations":[5,10,15],"ratios":["16:9","9:16"]},
  "sdas-hn-sd2.0-pro-933-720p": {"resolution":"720p","min":15,"max":15,"media":[9,3,3],"unit":"count","fixed":true,"ratios":["16:9","9:16","1:1","4:3","3:4","21:9"]},
  "sdas-ll-sd2.5-pro-30s-720p": {"resolution":"720p","min":30,"max":30,"media":[30,3,0],"unit":"count","fixed":true,"ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.0-1080p": {"resolution":"1080p","min":4,"max":15,"media":[9,3,3],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.0-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.0-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.0-fast-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.0-fast-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.0-mini-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.0-mini-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.5-1080p": {"resolution":"1080p","min":4,"max":30,"media":[10,5,5],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.5-480p": {"resolution":"480p","min":4,"max":30,"media":[30,10,10],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mg-sd2.5-720p": {"resolution":"720p","min":4,"max":30,"media":[30,10,10],"unit":"second","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-mj-minimax-h3-2k": {"resolution":"2k","min":4,"max":15,"media":[9,0,3],"unit":"count","ratios":["1:1","16:9","9:16","3:4","4:3","21:9"],"schedulable":false},
  "sdas-pd-sd2.0-mini-903-480p": {"resolution":"480p","min":5,"max":15,"media":[9,0,3],"unit":"count","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-pd-sd2.0-mini-903-720p": {"resolution":"720p","min":5,"max":12,"media":[9,0,3],"unit":"count","ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
  "sdas-qd-seedance-2.0-1080p": {"resolution":"1080p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-4k": {"resolution":"4k","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-fast-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-fast-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-fast-no-face-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-fast-no-face-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-mini-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-mini-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-mini-no-face-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-mini-no-face-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-no-face-1080p": {"resolution":"1080p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-no-face-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-no-face-4k": {"resolution":"4k","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.0-no-face-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.5-1080p": {"resolution":"1080p","min":4,"max":30,"media":[30,10,10],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.5-480p": {"resolution":"480p","min":4,"max":30,"media":[30,10,10],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-qd-seedance-2.5-720p": {"resolution":"720p","min":4,"max":30,"media":[30,10,10],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-wd-sd2.0-480p": {"resolution":"480p","min":4,"max":15,"media":[9,0,3],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-wd-sd2.0-720p": {"resolution":"720p","min":4,"max":15,"media":[9,0,3],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-wd-sd2.0-fast-480p": {"resolution":"480p","min":4,"max":15,"media":[9,0,3],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-wd-sd2.0-fast-720p": {"resolution":"720p","min":4,"max":15,"media":[9,0,3],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-wd-sd2.5-480p": {"resolution":"480p","min":4,"max":30,"media":[30,0,10],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-wd-sd2.5-720p": {"resolution":"720p","min":4,"max":30,"media":[30,0,10],"unit":"second","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-wf-sd2.0-mini-933-480p": {"resolution":"480p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-wf-sd2.0-mini-933-720p": {"resolution":"720p","min":4,"max":12,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-xg-sd2.0-pro-933-720p": {"resolution":"720p","min":4,"max":15,"media":[9,3,3],"unit":"count","ratios":["1:1","3:4","4:3","9:16","16:9","21:9"]},
  "sdas-xh-wan3.0-2-720p": {"resolution":"720p","min":4,"max":15,"media":[10,5,5],"unit":"second","ratios":["16:9","9:16","1:1","4:3","3:4","21:9"]},
  "sdas-xh-wan3.0-720p": {"resolution":"720p","min":4,"max":30,"media":[10,0,5],"unit":"second","ratios":["16:9","9:16","1:1","4:3","3:4","21:9"]},
  "sdas-xl-sd2.0-903-mini-480p": {"resolution":"480p","min":15,"max":15,"media":[9,0,3],"unit":"count","fixed":true,"ratios":["21:9","16:9","4:3","1:1","3:4","9:16"]},
};
const RESOLUTIONS = ["480p", "720p", "1080p", "2k", "4k"];
const RATIOS = ["1:1", "3:4", "4:3", "9:16", "16:9", "21:9", "adaptive"];
const MEDIA_GROUPS = [
  ["imageUrls", "images", "referenceImages", "reference_images", "image", "input_reference"],
  ["videoUrls", "videos", "referenceVideos", "reference_videos"],
  ["audioUrls", "audios", "referenceAudios", "reference_audios"],
];
const MEDIA_FIELDS = MEDIA_GROUPS.flat();
const PAYLOAD_FIELDS = ["aspectRatio", "mode", "imageUrls", "videoUrls", "audioUrls", "firstFrameUrl", "lastFrameUrl", "bypassCopyrightReferenceLevel"];
const FIELDS = ["model", "prompt", "seconds", "duration", "resolution", "size", "n", "ratio", "aspect_ratio", "metadata"].concat(PAYLOAD_FIELDS, MEDIA_FIELDS);
const USAGE_SCHEMA = {
  requests: { type: "number", unit: "count", unitLabel: { en: "video", zh: "条" }, description: { en: "Video generation per-video price", zh: "视频按条单价" } },
  seconds: { type: "number", unit: "second", description: { en: "Video generation per-second price", zh: "视频按秒单价" } },
  resolution: { enum: RESOLUTIONS, description: { en: "Output video resolution", zh: "输出视频分辨率" } },
};

export const meta = {
  apiVersion: 1,
  key: "sudashui",
  name: "苏打水 Sudashui",
  version: "1.0.0",
  author: { name: "jiayi-1994" },
  description: { en: "Video generation through Sudashui with purchase-cost scheduling", zh: "通过苏打水生成视频，支持采购成本调度" },
  icon: "text:苏打",
  website: "https://api-docs.sudashuiapi.com/",
  baseUrl: "https://api.sudashuiapi.com",
  models: Object.keys(MODELS),
  fetchMode: "per_task",
  protocols: ["openai_video"],
  // Quantities only: administrators configure prices and reference-media fees.
  usageSchema: USAGE_SCHEMA,
  usageProfiles: Object.keys(MODELS).map(function (name) {
    const model = MODELS[name];
    const quantity = model.unit === "second" ? "seconds" : "requests";
    return {
      models: [name],
      schema: { [quantity]: USAGE_SCHEMA[quantity], resolution: { ...USAGE_SCHEMA.resolution, enum: [model.resolution] } },
      examples: [{ label: model.resolution + " · " + model.min + "s", facts: { [quantity]: quantity === "seconds" ? model.min : 1, resolution: model.resolution } }],
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
  return typeof value === "string" && value.length <= 8192 && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value) && !/[\u0000-\u001f\u007f]/.test(value);
}

function duration(value) {
  if ((typeof value !== "number" && typeof value !== "string") || !/^\d+$/.test(String(value))) throw new Error("duration must be an integer from 4 to 30 seconds");
  const result = Number(value);
  if (!Number.isSafeInteger(result) || result < 4 || result > 30) throw new Error("duration must be an integer from 4 to 30 seconds");
  return result;
}

function resolution(value) {
  if (typeof value !== "string") throw new Error("resolution must be a string");
  const tier = value.toLowerCase() === "2160p" ? "4k" : value.toLowerCase();
  if (!RESOLUTIONS.includes(tier)) throw new Error("resolution must be one of " + RESOLUTIONS.join(", "));
  return tier;
}

function aspectRatio(value) {
  if (!RATIOS.includes(value)) throw new Error("aspectRatio must be one of " + RATIOS.join(", "));
  return value;
}

function mediaURLs(value, field) {
  if (typeof value === "string") {
    if (value.trim().startsWith("[")) {
      try { value = JSON.parse(value); } catch (_error) { throw new Error(field + " must be a URL array"); }
    } else value = [value];
  } else if (Array.isArray(value) && value.length === 1 && typeof value[0] === "string" && value[0].trim().startsWith("[")) {
    try { value = JSON.parse(value[0]); } catch (_error) { throw new Error(field + " must be a URL array"); }
  }
  // A local resource bound, not a claim about a model's reference capacity.
  if (!Array.isArray(value) || value.length > 50) throw new Error(field + " must contain at most 50 public URLs");
  return value.map(function (url) {
    if (!httpURL(url)) throw new Error(field + " entries must be HTTP(S) URLs without credentials");
    return url;
  });
}

function scalarAlias(input, names, parse) {
  let selected;
  for (const name of names) {
    if (!has(input, name)) continue;
    const value = parse(input[name]);
    if (selected !== undefined && selected !== value) throw new Error("conflicting values for " + names.join("/"));
    selected = value;
  }
  return selected;
}

// All submission hooks revalidate the same canonical body, including overrides.
function videoRequest(input) {
  if (!isObject(input)) throw new Error("video request must be an object");
  for (const key of Object.keys(input)) {
    if (!FIELDS.includes(key)) throw new Error("unsupported video parameter: " + key);
  }
  if (has(input, "n") && input.n !== 1 && input.n !== "1") throw new Error("n must be 1");
  if (has(input, "prompt") && typeof input.prompt !== "string") throw new Error("prompt must be a string");
  const seconds = scalarAlias(input, ["seconds", "duration"], duration);
  if (seconds === undefined) throw new Error("seconds or duration is required");

  let payload = {};
  if (has(input, "metadata")) {
    if (!isObject(input.metadata) || Object.keys(input.metadata).some(key => key !== "payload") || typeof input.metadata.payload !== "string") throw new Error("metadata must contain only a JSON string payload");
    try { payload = JSON.parse(input.metadata.payload); } catch (_error) { throw new Error("metadata.payload must be valid JSON"); }
    if (!isObject(payload)) throw new Error("metadata.payload must encode an object");
    for (const key of Object.keys(payload)) {
      if (!PAYLOAD_FIELDS.includes(key)) throw new Error("unsupported metadata.payload parameter: " + key);
    }
  }
  const merged = {};
  for (const key of Object.keys(input)) if (key !== "metadata") merged[key] = input[key];
  for (const key of Object.keys(payload)) {
    if (has(merged, key)) throw new Error("duplicate metadata.payload parameter: " + key);
    merged[key] = payload[key];
  }

  let tier = has(input, "resolution") ? resolution(input.resolution) : undefined;
  let ratio = scalarAlias(merged, ["aspectRatio", "aspect_ratio", "ratio"], aspectRatio);
  if (has(input, "size")) {
    if (typeof input.size !== "string") throw new Error("size must be a resolution or WIDTHxHEIGHT");
    let sizeTier;
    const match = /^([1-9]\d{2,4})x([1-9]\d{2,4})$/.exec(input.size);
    if (match) {
      const width = Number(match[1]);
      const height = Number(match[2]);
      sizeTier = resolution(Math.min(width, height) + "p");
      const sizeRatio = RATIOS.find(function (item) {
        if (item === "adaptive") return false;
        const parts = item.split(":");
        return Math.abs(width / height - Number(parts[0]) / Number(parts[1])) < 0.01;
      });
      if (!sizeRatio) throw new Error("size has an unsupported aspect ratio");
      if (ratio !== undefined && ratio !== sizeRatio) throw new Error("size conflicts with aspectRatio");
      ratio = sizeRatio;
    } else sizeTier = resolution(input.size);
    if (tier !== undefined && tier !== sizeTier) throw new Error("size conflicts with resolution");
    tier = sizeTier;
  }

  const mode = has(merged, "mode") ? merged.mode : "references";
  if (mode !== "references" && mode !== "frames") throw new Error("mode must be references or frames");
  const result = { duration: seconds, aspectRatio: ratio || "16:9", mode };
  if (has(merged, "bypassCopyrightReferenceLevel")) {
    const level = merged.bypassCopyrightReferenceLevel;
    if (typeof level !== "number" || !Number.isFinite(level) || level < 0.1 || level > 1) throw new Error("bypassCopyrightReferenceLevel must be a number between 0.1 and 1");
    result.bypassCopyrightReferenceLevel = level;
  }
  if (has(input, "prompt")) result.prompt = input.prompt;
  if (tier !== undefined) result.resolution = tier;
  for (const names of MEDIA_GROUPS) {
    const supplied = names.filter(name => has(merged, name));
    if (supplied.length > 1) throw new Error("provide only one reference field: " + names.join("/"));
    if (!supplied.length) continue;
    if (mode === "frames") throw new Error("frames mode cannot include reference arrays");
    result[names[0]] = mediaURLs(merged[supplied[0]], supplied[0]);
  }
  for (const name of ["firstFrameUrl", "lastFrameUrl"]) {
    if (mode === "references") {
      if (has(merged, name)) throw new Error("references mode cannot include frame URLs");
      continue;
    }
    if (!httpURL(merged[name])) throw new Error("frames mode requires a public " + name);
    result[name] = merged[name];
  }
  return result;
}

function modelRequest(ctx, decoding) {
  const model = ctx.upstreamModel || ctx.model;
  if (!has(MODELS, model)) throw new Error("unsupported Sudashui model: " + model);
  const capabilities = MODELS[model];
  // Initial decoding has no frozen sale yet. Enforce billing-unit compatibility
  // in driver hooks; unified sales own their price independently of the plugin.
  if (!decoding && ctx.salesSource !== "video_request" && has(MODELS, ctx.model) && MODELS[ctx.model].unit !== capabilities.unit) throw new Error("model mapping cannot change billing unit");
  const request = videoRequest(ctx.requestBody);
  const tier = capabilities.resolution;
  if (request.resolution !== undefined && request.resolution !== tier) throw new Error("resolution conflicts with upstream model " + model + " (" + tier + ")");
  if (request.duration < capabilities.min || request.duration > capabilities.max) throw new Error(model + " duration must be between " + capabilities.min + " and " + capabilities.max + " seconds");
  if (capabilities.durations && !capabilities.durations.includes(request.duration)) throw new Error(model + " duration must be " + capabilities.durations.join(", "));
  if (capabilities.ratios && !capabilities.ratios.includes(request.aspectRatio)) throw new Error(model + " aspectRatio must be " + capabilities.ratios.join(", "));
  const counts = [request.mode === "frames" ? 2 : (request.imageUrls || []).length, (request.videoUrls || []).length, (request.audioUrls || []).length];
  if (counts.some((count, index) => count > capabilities.media[index])) throw new Error(model + " reference limits are " + capabilities.media.join("/") + " images/videos/audios");
  if (capabilities.promptMax && Array.from(request.prompt || "").length > capabilities.promptMax) throw new Error(model + " prompt exceeds " + capabilities.promptMax + " characters");
  if (has(request, "bypassCopyrightReferenceLevel") && (!model.startsWith("sdas-qd-") || model.includes("no-face"))) throw new Error("bypassCopyrightReferenceLevel requires an sdas-qd model without no-face");
  request.resolution = tier;
  return { model, request };
}

function authHeaders(ctx) {
  const credential = ctx.authHeader || ctx.apiKey;
  if (typeof credential !== "string") throw new Error("channel API key is required");
  const token = credential.trim().replace(/^Bearer\s+/i, "").trim();
  if (!token || /\s/.test(token)) throw new Error("channel API key is required and must not contain whitespace");
  return { Authorization: "Bearer " + token };
}

export function buildSubmitRequest(ctx) {
  const normalized = modelRequest(ctx);
  const request = normalized.request;
  const payload = {};
  for (const key of PAYLOAD_FIELDS) if (has(request, key)) payload[key] = request[key];
  const body = { model: normalized.model, duration: request.duration, metadata: { payload: JSON.stringify(payload) } };
  if (has(request, "prompt")) body.prompt = request.prompt;
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/video/generations",
    method: "POST", headers: { ...authHeaders(ctx), "Content-Type": "application/json" }, body,
  };
}

export function extractUsage(ctx) {
  const normalized = modelRequest(ctx);
  const request = normalized.request;
  const quantity = MODELS[normalized.model].unit === "second" ? "seconds" : "requests";
  return { [quantity]: quantity === "seconds" ? request.duration : 1, resolution: request.resolution };
}

export function extractUsageOnComplete() {
  // The documented result supplies no measured duration or billing usage.
  // Keep the validated reservation, never infer usage from progress or credits.
  return {};
}

export function describeSpec(ctx) {
  const normalized = modelRequest(ctx);
  const capabilities = MODELS[normalized.model];
  if (capabilities.schedulable === false) return { unsupported: true };
  const request = normalized.request;
  return {
    spec_version: 2, output_seconds: request.duration, seconds_kind: capabilities.fixed ? "fixed" : "exact", resolution: request.resolution,
    references: { image: request.mode === "frames" ? 2 : (request.imageUrls || []).length, video: (request.videoUrls || []).length, audio: (request.audioUrls || []).length },
    reference_video_urls: request.videoUrls || [],
  };
}

function taskData(body) {
  if (!isObject(body)) return {};
  if (has(body, "code")) return body.code === "success" && isObject(body.data) ? body.data : {};
  return body;
}

function videoURL(body) {
  const data = taskData(body);
  if (httpURL(data.result_url)) return data.result_url;
  const details = isObject(data.data) ? data.data : {};
  const creations = Array.isArray(details.creations) ? details.creations : [];
  for (const creation of creations) if (isObject(creation) && httpURL(creation.url)) return creation.url;
  if (httpURL(details.volcesVideoUrl)) return details.volcesVideoUrl;
  return "";
}

export function parseTaskResult(ctx, body) {
  const data = taskData(body);
  // Query rows also have a numeric database id; only task_id identifies the job.
  const returnedID = has(data, "task_id") ? data.task_id : typeof data.id === "string" ? data.id : undefined;
  if (ctx.taskId && returnedID !== undefined && returnedID !== ctx.taskId) return { status: "UNKNOWN", reason: "upstream task id does not match the queried task" };
  const details = isObject(data.data) ? data.data : {};
  const states = { queued: "QUEUED", submitted: "QUEUED", queueing: "QUEUED", in_progress: "IN_PROGRESS", processing: "IN_PROGRESS", completed: "SUCCESS", success: "SUCCESS", failed: "FAILURE", failure: "FAILURE" };
  const raw = typeof data.status === "string" ? data.status.toLowerCase() : "";
  const inner = typeof details.state === "string" ? details.state.toLowerCase() : "";
  const selected = raw || inner;
  if (!has(states, selected) || (raw && inner && (!has(states, inner) || states[raw] !== states[inner]))) return { status: "UNKNOWN", reason: "unrecognized or conflicting Sudashui task status" };
  const status = states[selected];
  if (status === "SUCCESS" && !videoURL(body)) return { status: "UNKNOWN", reason: "successful task has no usable video URL" };
  const result = { status };
  if ((typeof data.progress === "string" && /^\d+(?:\.\d+)?%?$/.test(data.progress)) || typeof data.progress === "number") {
    const progress = Number(String(data.progress).replace(/%$/, ""));
    if (Number.isFinite(progress) && progress >= 0 && progress <= 100) result.progress = progress + "%";
  }
  if (status === "FAILURE") {
    const reason = [data.fail_reason, details.fail_reason, details.error_message, details.message, details.err_code].find(value => typeof value === "string" && value.trim());
    result.reason = (details.err_code === "invalid_request" ? "invalid_request: " : "") + (reason || "video generation failed");
  }
  return result;
}

export function parseSubmitResponse(_ctx, response) {
  const body = response.body;
  if (!isObject(body)) throw new Error("invalid Sudashui create response");
  const data = taskData(body);
  const ids = [data.task_id, data.id, body.task_id, body.id].filter(value => value !== undefined && value !== null);
  if (ids.some(value => typeof value !== "string" || !value.trim() || value !== value.trim())) throw new Error("invalid upstream task id");
  const taskId = ids[0];
  if (ids.some(value => value !== taskId)) throw new Error("conflicting upstream task ids");
  if (has(body, "code") && body.code !== "success") {
    if (taskId || body.data != null || body.status != null) throw new Error("conflicting Sudashui acceptance and rejection signals");
    // The documented error wraps an explicit failed state in a JSON string.
    // A generic fail_to_fetch_task could hide accepted work, so never retry it.
    let failure;
    try { failure = JSON.parse(body.message); } catch (_error) { throw new Error("ambiguous Sudashui create error"); }
    if (body.code !== "fail_to_fetch_task" || !isObject(failure) || failure.state !== "failed" || typeof failure.message !== "string" || !failure.message.trim() || has(failure, "id") || has(failure, "task_id") || !Array.isArray(failure.creations) || failure.creations.length) throw new Error("ambiguous Sudashui create error");
    const code = typeof failure.err_code === "string" && failure.err_code ? failure.err_code + ": " : "";
    return { rejected: { reason: code + failure.message } };
  }
  if (!taskId) throw new Error("upstream create response has no task id");
  if (body.error || body.success === false) throw new Error("conflicting Sudashui acceptance and rejection signals");
  const output = { taskId, taskData: body };
  const result = parseTaskResult({ taskId }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") output.immediate = result;
  return output;
}

export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !ctx.taskId) throw new Error("upstream task id is required");
  return { url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/video/generations/" + encodeURIComponent(ctx.taskId), method: "GET", headers: authHeaders(ctx) };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && videoURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const url = videoURL(ctx.data);
  if (ctx.artifactKey !== "video" || !url) throw new Error("artifact_not_found");
  // The host enforces SSRF/redirect checks; storage receives no API credentials.
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export function classifyFailure(reason) {
  const text = String(reason || "").toLowerCase();
  if (/timeout|timed out|service (is temporarily )?unavailable|internal (server )?error|quota (exhausted|exceeded)|insufficient (quota|balance)|rate limit|connection|authentication|unauthorized|permission denied|超时|服务(暂)?不可用|内部错误|(?:额度|余额)(不足|耗尽)|限流/.test(text)) return "upstream";
  if (/cancell?ed by (the )?user\b/.test(text)) return "cancelled";
  if (/^invalid_request:|real human faces are not supported|content policy violation|content violation|moderation (rejected|blocked)|审核(未通过|不通过|拒绝)|未通过(内容)?审核|内容.{0,12}(违规|敏感|不合规)|invalid (image|video|audio|prompt|url)\b/.test(text)) return "user";
  return "upstream";
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      const body = ctx.body;
      let input;
      if (body && body.kind === "json") input = body.value;
      else if (body && body.kind === "multipart") {
        if ((body.files || []).length) throw new Error("file uploads require the Sudashui upload API first; send public media URLs");
        input = {};
        for (const rawName of Object.keys(body.fields || {})) {
          const name = rawName.endsWith("[]") ? rawName.slice(0, -2) : rawName;
          if (!FIELDS.includes(name)) throw new Error("unsupported video parameter: " + name);
          if (has(input, name)) throw new Error("duplicate video parameter: " + name);
          const entries = body.fields[rawName];
          if (!Array.isArray(entries) || !entries.length) throw new Error(name + " requires a value");
          if (MEDIA_FIELDS.includes(name)) input[name] = entries;
          else {
            if (entries.length !== 1) throw new Error(name + " must be provided once");
            input[name] = entries[0];
            if (name === "metadata" || name === "bypassCopyrightReferenceLevel") {
              try { input[name] = JSON.parse(entries[0]); } catch (_error) { throw new Error(name + " must be valid JSON"); }
            }
          }
        }
      } else throw new Error("JSON or multipart body required");
      if (!isObject(input) || typeof input.model !== "string" || !input.model.trim()) throw new Error("model is required");
      if (ctx.model && input.model !== ctx.model) throw new Error("model does not match the selected model");
      const requestBody = videoRequest(input);
      // Initial candidate decoding precedes channel mapping. Only the mapped
      // driver/spec can require a particular upstream model and resolution.
      if (ctx.upstreamModel) modelRequest({ model: input.model, upstreamModel: ctx.upstreamModel, requestBody }, true);
      const references = requestBody.mode === "frames" || MEDIA_GROUPS.some(names => (requestBody[names[0]] || []).length);
      return { kind: "submit", model: input.model, action: references ? "reference_to_video" : "text_to_video", requestBody };
    },
    render: function (_ctx, task) {
      // Public task identity, status and error envelope belong to the host.
      const url = task.status === "SUCCESS" ? videoURL(task.data) : "";
      return url ? { url } : {};
    },
  },
};
