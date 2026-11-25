# Qwen3.5 with its template's default: thinking on (the generation prompt
# opens <think>). reasoning_effort is ignored by this template, so none is
# sent. Sampling: Qwen's documented thinking-mode settings.
p = provider("openai", base_url = "https://llama-server.example/v1", api_key = env("SPARK_API_KEY"))
default = "m"
models = {"m": model(p, "unsloth/Qwen3.5-4B-GGUF:Q4_K_M", context = 65536, max_output = 32768,
    temperature = 0.6,
    extra_params = {"top_p": 0.95, "top_k": 20, "chat_template_kwargs": {"enable_thinking": True}})}
retry_timeout = 3600
