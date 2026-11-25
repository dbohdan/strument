p = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "m"
models = {"m": model(p, "google/gemma-4-26b-a4b-it", context = 131072, reasoning = "low", cache = True, max_output = 32768, extra_params = {"provider": {"quantizations": ["bf16"], "allow_fallbacks": False}})}
