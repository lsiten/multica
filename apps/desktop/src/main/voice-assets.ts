// Pinned multilingual Whisper base ONNX weights (Apache-2.0), never user-supplied URLs.
export const VOICE_REVISION = "64da57285918e20ea79ea5c88eed7197933abaa8";
export const VOICE_BASE_URL = "https://huggingface.co/Xenova/whisper-base/resolve/" + VOICE_REVISION + "/";
export const VOICE_ASSETS = [
  {
    "name": "config.json",
    "size": 2248,
    "sha256": "d1d347fdb422e6347c2f843a90d375aa67ea3f4b3e20d2c3075f9a9f6243685b"
  },
  {
    "name": "generation_config.json",
    "size": 3776,
    "sha256": "3bba359e33fdd6dc1c10f71846a477d339b0242f462f70ea1dd73274caa38d05"
  },
  {
    "name": "preprocessor_config.json",
    "size": 339,
    "sha256": "a6a76d28c93edb273669eb9e0b0636a2bddbb1272c3261e47b7ca6dfdbac1b8d"
  },
  {
    "name": "tokenizer.json",
    "size": 2480466,
    "sha256": "27fc476bfe7f17299480be2273fc0608e4d5a99aba2ab5dec5374b4482d1a566"
  },
  {
    "name": "tokenizer_config.json",
    "size": 282683,
    "sha256": "2a4c4281cf9f51ac6ccc406fdc711a087afe6530f671fa7b80953edc498275ce"
  },
  {
    "name": "onnx/encoder_model_quantized.onnx",
    "size": 23200850,
    "sha256": "3e345e977b55620a37c0c2b2af0644e019afdfad562dcf71eb929bb7274285f9"
  },
  {
    "name": "onnx/decoder_model_merged_quantized.onnx",
    "size": 53707539,
    "sha256": "a6beb6baabb66f00b6a686d828c95ffca6146d51900cbad0266cad38f64cf861"
  }
] as const;
