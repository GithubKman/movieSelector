# Tool to search for and select a movie
## Objectives
 - Easily search and find movies
 - Select multiple at a time
 - Notify maintainer/ai agent to be torrented or ripped from blu-ray
 - Look nice with large movie thumbnails from search results
 - Output movies titles/other identifying info in a standard format
 - Easy to use management mode which allows movies to be marked as in progress or completed
    - Api that agent can use to automatically query and use to download media and manage movies
 - Very lightweight so I don't have to worry about resource use
## What this is
A simple website that allows users to search for movies and shows to add to a queue 
which will be saved on a database like sqlite to be downloaded to a nas by a maintainer
or an ai agent. Will primarily be ran inside of a docker container to be used with TrueNAS
