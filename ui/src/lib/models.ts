// The Claude models offered wherever a model is picked (a profile's model and allowed models in
// Settings and the profile editor). A profile's allowed models then decide what the New agent
// form offers. Fable first: the ladder is Fable for hard work, then Opus, Sonnet, Haiku
// (the owner #11, 17 Nov: "Fable 5.2 missing from the model choices"). Any other ID can still be
// typed in; lib/format.ts modelName() gives each its short name.
export const KNOWN_MODELS = ["claude-fable-5-2", "claude-opus-5-5", "claude-sonnet-5", "claude-haiku-4-5-20251001"];
