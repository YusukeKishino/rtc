.PHONY: asset
asset:
	cd frontend && \
	NODE_ENV=production yarn run build
