BACKEND_DIR=backend
FRONTEND_DIR=frontend

.PHONY: up down backend-test backend-vet backend-format-check frontend-test frontend-lint test lint fmt frontend-install

up:
	docker compose up --build

down:
	docker compose down -v

backend-test:
	cd $(BACKEND_DIR) && go test ./...

backend-vet:
	cd $(BACKEND_DIR) && go vet ./...

backend-format-check:
	cd $(BACKEND_DIR) && test -z "$$(gofmt -l .)"

frontend-install:
	cd $(FRONTEND_DIR) && npm install

frontend-test:
	cd $(FRONTEND_DIR) && npm test

test: backend-test frontend-test

frontend-lint:
	cd $(FRONTEND_DIR) && npm run lint

lint: backend-vet backend-format-check frontend-lint

fmt:
	cd $(BACKEND_DIR) && gofmt -w $$(find . -name '*.go')
	cd $(FRONTEND_DIR) && npm run format
