# action-deployer

A simple tool for deploying GitHub Actions artifacts to test server.

Only for testing purposes.

## Features

- Check the GitHub Actions for latest artifact for each job every 5 minutes.
- Use MurMurHash3 to check if each file in the zip archive is identical to the file under deployPath.
- Automatically update files with inconsistent hash value or just missing.

## Usage

- job.json

```json
[
    {
        "owner": "username",
        "repo": "reponame",
        "artifactName": "dist",
        "excludes": [
            "data.json",
            "json/.*",
            "useRegexHere",
            "iDontWantUpdateThisFile",
        ],
        "deployPath": "/tmp/"
    }
]
```

- secret.json

```json
[
    {
        "owner": "username",
        "token": "yourGitHubToken"
    }
]
```

