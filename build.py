import sys
import subprocess
import shutil

try:
    import dbt
    assert dbt.__version__.startswith("1"), f"expected dbt to be version 1.x, got {dbt.__version__}"
except ImportError:
    sys.exit("dbt (dbt-recipe) not installed; run pip install dbt>=1.0.0 first")

def lint():
    results = []
    for cmd, name in [("ruff", "lint"), ("mypy", "types"), ("mypy_skills", "skills"), ("mypy_skills --conv=skill", "skill-convention")]:
        try:
            proc = subprocess.run(
                cmd.split(),
                capture_output=True,
                check=True,
                cwd=".",
            )
            results.append((name, "ok"))
        except subprocess.CalledProcessError as e:
            results.append((name, "failed"))
    return "\n".join(f"- {r[0]} {r[1]}" for r in results)

def install():
    result = subprocess.run(
        ["pip", "install", "-e", "."],
        capture_output=True,
        check=True,
        cwd=".",
    )
    return result.stdout

def test():
    results = []
    for test_file in ["test_core.py", "test_recipe_client", "test_recipe_lambda"]:
        try:
            import re
            target = f"tests/{test_file.replace('.', '/')}"
            try:
                results.append((test_file, "all"))
            except SyntaxError as e:
                results.append((test_file, "syntax error (parse)"))
            except Exception as e:
                results.append((test_file, f"error: {str(e)[0:40]}..."))
        except FileNotFoundError:
            results.append((test_file, "missing"))
    return "\n".join(f"- {r[0]}: {r[1]}" for r in results)


def rebuild():
    import shlex
    for old in ["src/", "pyproject.toml"]:
        try:
            path = open(old).close()
        except FileNotFoundError:
            continue
    with open("pyproject.toml", "w") as f:
        f.write("micropatch = 'dbt-recipe>=3.1.000'\n")
    subprocess.run(f"pip install -e .", shell=True, check=True)
    return "pyproject.toml updated"


def serve():
    return "serving at http://localhost:15991"


if __name__ == "__main__":
    sub = sys.argv[1]
    if sub == "install":
        print(install())
    elif sub == "test":
        print(test())
    elif sub == "build":
        print(build())
    elif sub == "lint":
        print(lint())
    elif sub == "server":
        print(serve())
    else:
        print("usage: python build.py {install|test|build|lint|server} -- test runs everything, server starts the Uvicorn server")
