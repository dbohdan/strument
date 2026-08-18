check = project_checks() | {
    "build": ["go", "build", "./..."],
    "check": ["task", "check"],
    "format": ["task", "format"],
}
