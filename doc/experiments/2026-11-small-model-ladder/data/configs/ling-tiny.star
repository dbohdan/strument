p = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "m"
models = {"m": model(p, "inclusionai/ling-3.0-tiny", context = 131072, reasoning = "low", cache = True, max_output = 32768)}
