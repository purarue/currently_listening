from pathlib import Path
from setuptools import setup, find_packages

long_description = Path("README.md").read_text()
reqs = Path("requirements.txt").read_text().strip().splitlines()

pkg = "currently_listening_py"
setup(
    name=pkg,
    version="0.1.0",
    url="https://github.com/purarue/currently_listening_py",
    author="purarue",
    description=("""Local server module for mpv-history-daemon"""),
    long_description=long_description,
    long_description_content_type="text/markdown",
    license="MIT",
    packages=find_packages(include=[pkg]),
    install_requires=reqs,
    package_data={pkg: ["py.typed"]},
    zip_safe=False,
    keywords="",
    python_requires=">=3.10",
    entry_points={
        "console_scripts": [
            "currently_listening_py = currently_listening_py.__main__:main"
        ]
    },
    extras_require={
        "testing": [
            "mypy",
            "flake8",
        ]
    },
    classifiers=[
        "License :: OSI Approved :: MIT License",
        "Programming Language :: Python",
        "Programming Language :: Python :: 3",
        "Programming Language :: Python :: 3.10",
        "Programming Language :: Python :: 3.11",
    ],
)
