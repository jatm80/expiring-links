# expiring-link

Go app to create links that expire after a while.

## For local testing use the following Docker compose:

```
services:
  db:
    image: redis:latest
    ports:
      - "6379:6379"
  app:
    build: .
    environment:
      - PORT=3000
      - REDIS_URL=redis://:@db:6379/1
    ports:
      - "3000:3000"
    depends_on:
      - db
```

## For kubernetes deployment:
- cd deploy/
- helm dependency update expiring-links
- helm install expiring-links expiring-links

Inspired by [dustinmoris/self-destruct-notes](https://github.com/dustinmoris/self-destruct-notes)