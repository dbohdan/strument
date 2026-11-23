cc = provider("openai", base_url = "https://api.commandcode.ai/provider/v1", api_key = env("COMMAND_CODE_API_KEY"))
default = "inkling"
models = {"inkling": model(cc, "thinkingmachines/inkling-2-mini", context = 131072, reasoning = "low", cache = True)}
