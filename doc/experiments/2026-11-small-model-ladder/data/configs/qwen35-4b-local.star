# Qwen3.5's template ignores reasoning_effort and opens <think> unless
# enable_thinking is false, so thinking is switched off through the template.
# Sampling: Qwen's documented non-thinking settings.
p = provider("openai", base_url = "https://llama-server.example/v1", api_key = env("SPARK_API_KEY"))
default = "m"
models = {"m": model(p, "unsloth/Qwen3.5-4B-GGUF:Q4_K_M", context = 65536, max_output = 32768,
    temperature = 0.7,
    extra_params = {"top_p": 0.8, "top_k": 20, "chat_template_kwargs": {"enable_thinking": False}})}
retry_timeout = 3600
