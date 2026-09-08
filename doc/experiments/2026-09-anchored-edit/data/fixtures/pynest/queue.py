"""A small retry queue."""


class RetryQueue:
    def __init__(self, limit=5):
        self.limit = limit
        self.items = []
        self.failures = {}

    def push(self, item):
        if len(self.items) < self.limit:
            self.items.append(item)
            return True
        return False

    def drain(self, handler):
        done = []
        for item in list(self.items):
            try:
                handler(item)
            except Exception:
                count = self.failures.get(item, 0)
                if count > self.limit:
                    self.items.remove(item)
                else:
                    self.failures[item] = count + 1
                continue
            done.append(item)
            self.items.remove(item)
        return done
