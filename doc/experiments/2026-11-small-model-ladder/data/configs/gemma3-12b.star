p = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "m"
models = {"m": model(p, "google/gemma-3-12b-it", context = 131072, cache = True, max_output = 32768)}
