p = provider("openrouter", base_url = "https://openrouter.ai/api/v1", api_key = env("OPENROUTER_API_KEY"))
default = "m"
models = {"m": model(p, "ibm-granite/granite-4.2-8b", context = 131072, reasoning = "low", cache = True, max_output = 32768,
    extra_params = {"provider": {"only": ["coreweave"], "allow_fallbacks": False}})}
