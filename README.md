<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Distributed-Cache-System (Documentation in progress)](#distributed-cache-system-documentation-in-progress)
  - [Features](#features)
  - [Architecture](#architecture)
  - [Usage](#usage)
  - [Configuration](#configuration)
  - [Contributing](#contributing)
  - [License](#license)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Distributed-Cache-System (Documentation in progress)
A simple implementation of distributed cache system

## Features

- Scalable distributed cache system
- Consistent hashing for efficient key mapping
- Master server scaled up to multiple containers
- Load balancing using Nginx
- Auxiliary (aux) servers for caching data
- Remap cache data when a node is added/removed or on catastrophic failure
- Backup for catastrophic failure of all aux servers
- Docker containerization for easy deployment
- Metrics monitoring with Prometheus
- Visualization with Grafana
- LRU (Least Recently Used) caching algorithm implemented using Doubly Linked List (DLL) and Hashmap

## Architecture

The Distributed Cache System consists of the following components:

- **Master Server**: Accepts GET and POST requests from clients. It utilizes consistent hashing to map keys to specific auxiliary servers for efficient caching and retrieval.

- **Auxiliary (Aux) Servers**: Responsible for storing and managing cached data. Each auxiliary server utilizes an LRU cache implementation based on a Doubly Linked List (DLL) and Hashmap data structure.

- **Docker**: The project utilizes Docker to containerize and deploy the master and auxiliary servers, making it easy to scale and manage the system.

- **Load Balancing**: Nginx is used as a load balancer to distribute incoming requests among multiple instances of the master server. This ensures high availability and optimal utilization of resources.

- **Metrics Monitoring**: Prometheus is integrated into the system to collect and store metrics about various components, such as server performance, cache hit/miss ratios, and system health.

- **Visualization**: Grafana is used to visualize the collected metrics from Prometheus, providing insightful dashboards and graphs for monitoring the cache system's performance.


## Usage

1. Clone the repository:
   ```
   git clone https://github.com/cruzelx/Distributed-Cache-System.git
   ``` 
2. Build and run the Docker containers for the master and auxiliary servers:
   ```
    docker-compose up --build
   ```



## Configuration

- To adjust the number of auxiliary servers, modify the `docker-compose.yml` file and add/remove auxiliary server instances as needed.

- The cache system's behavior, such as cache size, eviction policies, and request timeouts, can be configured in the server codebase.

## Contributing

Contributions to the Distributed Cache System are welcome! If you find any issues or have suggestions for improvements, please submit a GitHub issue or create a pull request.

## License

This project is licensed under the [MIT License](LICENSE).