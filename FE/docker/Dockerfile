FROM node:20-alpine
WORKDIR /app
COPY package*.json ./
RUN npm install
# source mounted as volume at runtime for hot reload
# node_modules kept in named volume to avoid host override
CMD ["npm", "run", "dev"]
