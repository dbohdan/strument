p = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "m"
models = {"m": model(p, "qwen/qwen3.5-9b", context = 131072, reasoning = "low", cache = True, max_output = 32768, extra_params = {"provider": {"quantizations": ["fp4", "int4"], "allow_fallbacks": False}})}
