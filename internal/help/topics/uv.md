# Python projects and uv

Choose uv project for a script that belongs to a Python project. The picker
offers detected pyproject.toml/workspace locations; select the intended project.
lazycrontab generates --project and uses an explicit working directory, so you
do not need to remember the flags.

Choose uv script for a standalone script, including PEP 723 inline dependency
metadata. That environment is separate from surrounding project dependencies.
Choose Python with an existing .venv/bin/python if you want that interpreter
directly without uv preparing an environment.

Current uv discovers a project from the script directory for `uv run script.py`;
for other commands discovery starts at the working directory. --project chooses
the project discovery location but does not change the process working directory.
--directory changes the working directory. Project/workspace choices are shown
explicitly rather than guessed silently.

Normal uv run may synchronize dependencies or download a Python interpreter
before running. The selected host needs the required files, permissions and
network/cache availability. lazycrontab's preflight does not invoke uv run or
uv sync and cannot certify that all dependencies are installed.

Official reference: https://docs.astral.sh/uv/reference/cli/
Scripts: https://docs.astral.sh/uv/guides/scripts/
