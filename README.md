# Task Management System

This project is a task management system built using Encore and Go, following a microservices architecture. It allows users to create boards, manage tasks, and handle user authentication.

## Problem Breakdown

The task management system addresses the following problems:
1. **User Management**: Users need to sign up, log in, and manage their profiles securely.
2. **Board Management**: Users should be able to create boards, invite other users, and manage board members.
3. **Task Management**: Users need to create, update, delete, and list tasks associated with boards.
4. **Access Control**: Different roles (Admin, Member, Viewer) should have different permissions.

## Design Decisions

- **Microservices Architecture**: The system is divided into multiple services (user, board, task) to promote separation of concerns and scalability.
- **Database per Service**: Each service has its own database to ensure data isolation and independence.
- **JWT Authentication**: JSON Web Tokens (JWT) are used for secure user authentication and authorization.
- **Pub/Sub for Events**: The system uses a publish/subscribe model to handle events like board deletions, ensuring that related tasks are also deleted.

## Architecture

The architecture consists of the following services:
- **User Service**: Manages user authentication and user data.
- **Board Service**: Handles board creation, membership, and invitations.
- **Task Service**: Manages tasks associated with boards.

Each service communicates with its own database and uses Encore's built-in features for deployment and scaling.

## Database Migrations

The project uses SQL migrations to manage database schema changes. The migrations are located in the `migrations` directory for each service.


## Instructions to Run the Service

1. **Clone the Repository**:
   ```bash
   git clone https://github.com/raman20/task-flow.git
   cd task-flow
   ```

2. **Install Encore**:
   Follow the instructions on the [Encore website](https://encore.dev/docs/getting-started) to install Encore.


3. **Start the Services**:
   Use the Encore CLI to start the services:
   ```bash
   encore run
   ```

4. **Access the API**:
   API gateway:     http://127.0.0.1:4000
   Development Dashboard URL:  http://127.0.0.1:9400
