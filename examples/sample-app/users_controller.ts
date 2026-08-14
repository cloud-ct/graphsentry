// A tiny Express controller, part of the GraphSentry sample app.
import express from "express";

const router = express.Router();

router.post("/users", (req, res) => {
	userService.createUser(req.body);
	res.status(201).send();
});

class UsersController {
	create(req, res) {
		this.userService.createUser(req.body);
	}
}
